package application

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

// thresholdStoreStub 是决策单测的最小 domain.CheckpointStore：只满足
// shouldAutoCheckpoint 的 store 存在性要求，不执行真实 append。
type thresholdStoreStub struct{}

func (thresholdStoreStub) AppendCheckpoint(context.Context, string, domain.WorkingMemoryCheckpointV1) (domain.CheckpointSnapshot, error) {
	return domain.CheckpointSnapshot{}, nil
}

func (thresholdStoreStub) LoadLatestCheckpoint(context.Context, string) (domain.CheckpointSnapshot, error) {
	return domain.CheckpointSnapshot{}, domain.ErrCheckpointNotFound
}

func (thresholdStoreStub) ListCheckpointEvents(context.Context, string, int) ([]domain.CheckpointEvent, error) {
	return nil, nil
}

// thresholdDecisionTracker 返回无 run 身份、带 stub store 的 tracker；决策
// 路径不读取 run 字段。fresh tracker 的 T1 level 从首级、字节/consumed 水位从
// 0 开始（进程本地，restart 语义见 TestThresholdDecision_GuardsAndRestartReset）。
func thresholdDecisionTracker() *resilientRunState {
	return newResilientRunState(thresholdStoreStub{}, domain.Run{}, "")
}

// appendPendingToolObservation 追加一条 strict-JSON/pending tool_observation
// 并返回其 formatted block 字节（含 kind/sequence wrapper）。pending 项进入
// textual continuation 通道，是 unit-level 构造累计字节与 T2 身份的标准路径
// （与 AppendToolResult 的 strict-JSON 分支同语义）。
func appendPendingToolObservation(conversation *AgentConversation, content string) int {
	conversation.appendItem("tool_observation", content, true)
	item := conversation.pending[len(conversation.pending)-1]
	return len(formatConversationItem(item))
}

// appendPendingProtocolObservation 追加一条 pending protocol 观察（非工具内容），
// 用于在 unit-level 模拟“大工具观察之后的无关字节增长”。
func appendPendingProtocolObservation(conversation *AgentConversation, content string) {
	conversation.appendItem("protocol_observation", content, true)
}

// toolBlockContentFor 返回使单条 tool_observation 的 formatted block 恰好等于
// targetBytes 的内容字节数。只用于单条观察的边界测试（sequence 位数经
// nextSequence 精确推导，防止 target 低于 wrapper 开销）。
func toolBlockContentFor(conversation *AgentConversation, targetBytes int) int {
	sequence := conversation.nextSequence + 1
	prefix := len("tool_observation") + len(strconv.FormatInt(sequence, 10)) + 4
	return targetBytes - prefix
}

// growCumulativeBy 追加足够多的 pending protocol 观察，使累计字节自 start
// 增长至少 quantum。用于构造“通过 anti-spam 字节水位但低于下一个 T1 level”
// 的非工具增长。
func growCumulativeBy(conversation *AgentConversation, start int, quantum int) {
	for conversation.cumulativeBytes-start < quantum {
		appendPendingProtocolObservation(conversation, strings.Repeat("p", 32<<10))
	}
}

// appendExactToolBlock 追加若干条 pending tool_observation，使累计模型可见
// 字节精确等于 targetCumulative。maxContent 约束单条观察内容上限（默认受
// maxObservationBytes 约束）；T1 level 边界测试用小块构造，避免最后一条观察
// 自身越过 T2 输出压力线，把 T1/T2 判定分开验证。
func appendExactToolBlock(t *testing.T, conversation *AgentConversation, targetCumulative int) {
	appendExactToolBlockWithMax(t, conversation, targetCumulative, maxObservationBytes)
}

func appendExactToolBlockWithMax(t *testing.T, conversation *AgentConversation, targetCumulative, maxContent int) {
	t.Helper()
	for conversation.cumulativeBytes < targetCumulative {
		sequence := conversation.nextSequence + 1
		prefix := len("tool_observation") + len(strconv.FormatInt(sequence, 10)) + 4
		remaining := targetCumulative - conversation.cumulativeBytes - prefix
		if remaining < 0 {
			t.Fatalf("target cumulative %d is below current %d", targetCumulative, conversation.cumulativeBytes)
		}
		contentLength := remaining
		if contentLength > maxContent {
			contentLength = maxContent
		}
		appendPendingToolObservation(conversation, strings.Repeat("x", contentLength))
	}
	if conversation.cumulativeBytes != targetCumulative {
		t.Fatalf("cumulative = %d, want %d", conversation.cumulativeBytes, targetCumulative)
	}
}

// TestThresholdDecision_T1ContextPressureLevelBoundaries 证明 T1 context
// pressure 在累计模型可见字节达到 contextPressureStepBytes 的精确级别时触发，
// 且每级只触发一次（noteAutoCheckpoint 推进 level 后同级别不再重复）。
func TestThresholdDecision_T1ContextPressureLevelBoundaries(t *testing.T) {
	level := int(contextPressureStepBytes)
	// 小块（≤ 4 KiB）构造：最后一条观察远低于 T2 输出压力线，T1 level 判定
	// 与 T2 观察线互不干扰。
	build := func(t *testing.T, target int) *AgentConversation {
		conversation := NewAgentConversation("")
		appendExactToolBlockWithMax(t, conversation, target, 4000)
		if sequence, _ := conversation.LargeToolObservation(); sequence != 0 {
			t.Fatalf("test precondition: a small-block build created a large observation identity")
		}
		return conversation
	}
	t.Run("below first level", func(t *testing.T) {
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(build(t, level-1))
		if fire || contextPressure {
			t.Fatalf("below first level fired (fire=%v context=%v)", fire, contextPressure)
		}
	})
	t.Run("at first level", func(t *testing.T) {
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(build(t, level))
		if !fire || !contextPressure {
			t.Fatalf("at first level did not fire (fire=%v context=%v)", fire, contextPressure)
		}
	})
	t.Run("above first level", func(t *testing.T) {
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(build(t, level+1))
		if !fire || !contextPressure {
			t.Fatalf("above first level did not fire (fire=%v context=%v)", fire, contextPressure)
		}
	})
	t.Run("same level does not repeat after advance", func(t *testing.T) {
		tracker := thresholdDecisionTracker()
		conversation := build(t, level)
		fire, contextPressure := tracker.shouldAutoCheckpoint(conversation)
		if !fire || !contextPressure {
			t.Fatalf("first fire missing (fire=%v context=%v)", fire, contextPressure)
		}
		tracker.noteAutoCheckpoint(conversation, contextPressure)
		if tracker.thresholdCheckpointLevelBytes != 2*contextPressureStepBytes {
			t.Fatalf("level after advance = %d, want %d", tracker.thresholdCheckpointLevelBytes, 2*contextPressureStepBytes)
		}
		// 同一累计内容重复评估：水位已推进，绝不能 spam 第二次。
		if fire, _ := tracker.shouldAutoCheckpoint(conversation); fire {
			t.Fatal("same cumulative content fired twice")
		}
		// 未到第二级（还差一个字节）不触发；精确命中第二级触发一次。
		if fire, _ := tracker.shouldAutoCheckpoint(build(t, 2*level-1)); fire {
			t.Fatal("second level minus one byte fired")
		}
		fire, contextPressure = tracker.shouldAutoCheckpoint(build(t, 2*level))
		if !fire || !contextPressure {
			t.Fatalf("second level did not fire (fire=%v context=%v)", fire, contextPressure)
		}
	})
}

// TestThresholdDecision_T2ObservationPressureBoundaries 证明 T2 output
// pressure 由单条完整工具观察的块字节线决定：观察块恰在
// toolOutputPressureBytes 时建立大观察身份并触发；多个小观察即使累计越过同一
// 字节线也不触发（T2 的对象是单次完整工具观察，而非累计内容）。
func TestThresholdDecision_T2ObservationPressureBoundaries(t *testing.T) {
	quantum := int(toolOutputPressureBytes)
	t.Run("below observation line", func(t *testing.T) {
		conversation := NewAgentConversation("")
		block := appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, quantum-1)))
		if block != quantum-1 {
			t.Fatalf("observation block = %d, want %d", block, quantum-1)
		}
		if sequence, bytes := conversation.LargeToolObservation(); sequence != 0 || bytes != 0 {
			t.Fatalf("below-line observation created a large identity: %d/%d", sequence, bytes)
		}
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(conversation)
		if fire || contextPressure {
			t.Fatalf("below observation line fired (fire=%v context=%v)", fire, contextPressure)
		}
	})
	t.Run("at observation line", func(t *testing.T) {
		conversation := NewAgentConversation("")
		block := appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, quantum)))
		if block != quantum {
			t.Fatalf("observation block = %d, want %d", block, quantum)
		}
		sequence, bytes := conversation.LargeToolObservation()
		if sequence == 0 || bytes != quantum {
			t.Fatalf("at-line observation identity = %d/%d, want non-zero/%d", sequence, bytes, quantum)
		}
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(conversation)
		if !fire || contextPressure {
			t.Fatalf("at observation line did not fire (fire=%v context=%v)", fire, contextPressure)
		}
	})
	t.Run("above observation line", func(t *testing.T) {
		conversation := NewAgentConversation("")
		block := appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, quantum+100)))
		if block != quantum+100 {
			t.Fatalf("observation block = %d, want %d", block, quantum+100)
		}
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(conversation)
		if !fire || contextPressure {
			t.Fatalf("above observation line did not fire (fire=%v context=%v)", fire, contextPressure)
		}
	})
	t.Run("small observations do not trigger on cumulative alone", func(t *testing.T) {
		conversation := NewAgentConversation("")
		// 两条 25 KiB 观察累计越过 output 压力量子线，但单条观察本身仍低于
		// 输出压力线：必须不触发（T2 的对象是单次完整工具观察）。
		appendPendingToolObservation(conversation, strings.Repeat("x", 25000))
		appendPendingToolObservation(conversation, strings.Repeat("x", 25000))
		if conversation.CumulativeModelVisibleBytes() <= quantum {
			t.Fatalf("cumulative = %d, want above quantum %d", conversation.CumulativeModelVisibleBytes(), quantum)
		}
		if sequence, _ := conversation.LargeToolObservation(); sequence != 0 {
			t.Fatalf("small observations created a large identity: %d", sequence)
		}
		fire, contextPressure := thresholdDecisionTracker().shouldAutoCheckpoint(conversation)
		if fire || contextPressure {
			t.Fatalf("small observations fired (fire=%v context=%v)", fire, contextPressure)
		}
	})
}

// TestThresholdDecision_NoGrowthDoesNotSpam 证明 anti-spam 语义：自动 checkpoint
// 之后，同一大观察的重复评估与不足一个输出压力量子的增长都不会再次触发；只有
// 累计字节自水位增长 ≥ toolOutputPressureBytes 且出现新的大观察才允许下一次。
func TestThresholdDecision_NoGrowthDoesNotSpam(t *testing.T) {
	tracker := thresholdDecisionTracker()
	// 第一轮：一条量子级观察触发 T2，并完成 noteAutoCheckpoint（模拟成功
	// append；consumed 水位推进到该观察身份）。
	conversation := NewAgentConversation("")
	appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	fire, contextPressure := tracker.shouldAutoCheckpoint(conversation)
	if !fire || contextPressure {
		t.Fatalf("first output-pressure fire missing (fire=%v context=%v)", fire, contextPressure)
	}
	tracker.noteAutoCheckpoint(conversation, contextPressure)
	if tracker.consumedToolObservationSequence == 0 {
		t.Fatal("noteAutoCheckpoint did not consume the fired observation identity")
	}
	watermark := tracker.lastAutoCheckpointBytes

	// 多轮小增长（协议修正等，累计不足一个量程）不得再次触发。
	for index := 0; index < 8; index++ {
		appendPendingProtocolObservation(conversation, fmt.Sprintf("correction-%d", index))
		if conversation.cumulativeBytes-int(watermark) >= int(toolOutputPressureBytes) {
			t.Fatalf("test precondition lost: growth crossed the quantum after %d corrections", index)
		}
		if fire, _ := tracker.shouldAutoCheckpoint(conversation); fire {
			t.Fatalf("small no-growth conversation fired at correction %d", index)
		}
	}
	// 新的量子级观察（≥ 48 KiB 新内容）构成 meaningful growth，允许再触发一次。
	before := conversation.cumulativeBytes
	appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	if conversation.cumulativeBytes-before < int(toolOutputPressureBytes) {
		t.Fatalf("second observation did not add a full quantum")
	}
	fire, contextPressure = tracker.shouldAutoCheckpoint(conversation)
	if !fire || contextPressure {
		t.Fatalf("second quantum-level observation did not fire (fire=%v context=%v)", fire, contextPressure)
	}
}

// TestThresholdDecision_T2StaleObservationDoesNotRefireAfterNonToolGrowth 证明
// Defect-2 修正的核心场景：一条已由 T2 checkpoint 消费的大观察不能因后续
// ≥ 一个 quantum 的“非工具”内容增长（协议修正等，通过 anti-spam 字节水位但
// 低于下一个 T1 level）再次触发。旧实现只记忆 LastToolObservationBytes（最近
// 一次观察的字节，仍是 ≥ threshold 的陈旧值），会在该评估点错误地重新触发。
func TestThresholdDecision_T2StaleObservationDoesNotRefireAfterNonToolGrowth(t *testing.T) {
	tracker := thresholdDecisionTracker()
	conversation := NewAgentConversation("")
	appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	fire, contextPressure := tracker.shouldAutoCheckpoint(conversation)
	if !fire || contextPressure {
		t.Fatalf("first large observation did not fire (fire=%v context=%v)", fire, contextPressure)
	}
	tracker.noteAutoCheckpoint(conversation, contextPressure)
	consumed := tracker.consumedToolObservationSequence

	// ≥ quantum 的非工具 pending 内容增长（低于下一 T1 level）。
	growCumulativeBy(conversation, int(tracker.lastAutoCheckpointBytes), int(toolOutputPressureBytes))
	if conversation.CumulativeModelVisibleBytes() >= int(contextPressureStepBytes) {
		t.Fatalf("test precondition lost: non-tool growth crossed the T1 level")
	}
	if tracker.consumedToolObservationSequence != consumed {
		t.Fatal("non-tool growth consumed new observations unexpectedly")
	}
	if fire, contextPressure := tracker.shouldAutoCheckpoint(conversation); fire || contextPressure {
		t.Fatalf("consumed large observation re-fired after unrelated growth (fire=%v context=%v)", fire, contextPressure)
	}
	// 新的大观察仍可触发一次（consumed 水位只覆盖旧观察）。
	appendPendingToolObservation(conversation, strings.Repeat("y", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	if fire, _ := tracker.shouldAutoCheckpoint(conversation); !fire {
		t.Fatal("new large observation after unrelated growth did not fire")
	}
}

// TestThresholdDecision_TwoDistinctLargeObservationsFireTwice 证明两条各自独立
// 的大观察，在每条都先被一次 checkpoint 消费之后，共触发两次 T2（“two fires
// when growth allows”），且同一组内容不会触发第三次。
func TestThresholdDecision_TwoDistinctLargeObservationsFireTwice(t *testing.T) {
	tracker := thresholdDecisionTracker()
	conversation := NewAgentConversation("")
	appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	fire, contextPressure := tracker.shouldAutoCheckpoint(conversation)
	if !fire || contextPressure {
		t.Fatalf("first large observation did not fire (fire=%v context=%v)", fire, contextPressure)
	}
	tracker.noteAutoCheckpoint(conversation, contextPressure)

	appendPendingToolObservation(conversation, strings.Repeat("y", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	fire, contextPressure = tracker.shouldAutoCheckpoint(conversation)
	if !fire || contextPressure {
		t.Fatalf("second distinct large observation did not fire (fire=%v context=%v)", fire, contextPressure)
	}
	tracker.noteAutoCheckpoint(conversation, contextPressure)
	if fire, _ := tracker.shouldAutoCheckpoint(conversation); fire {
		t.Fatal("same two observations fired a third time")
	}
}

// TestThresholdDecision_ForcedCheckpointConsumesLargeObservation 证明“任何在其
// 之后的 durable checkpoint 都应消费/覆盖一条大观察”：一次强制 checkpoint
// （phase boundary / recovery / threshold / process shutdown）落盘后，即使累计
// 字节再增长一个 quantum 的非工具内容，同一条大观察也不会触发 T2（那时该观察
// 的 working memory 已随 checkpoint 落盘）；只有 checkpoint 之后新出现的大观察
// 仍可触发。
func TestThresholdDecision_ForcedCheckpointConsumesLargeObservation(t *testing.T) {
	tracker := thresholdDecisionTracker()
	conversation := NewAgentConversation("")
	appendPendingToolObservation(conversation, strings.Repeat("x", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	// 强制 checkpoint 先于任何自动评估落盘（不经 shouldAutoCheckpoint）。
	tracker.consumeConversationWatermarks(conversation)
	if tracker.consumedToolObservationSequence == 0 {
		t.Fatal("forced checkpoint did not consume the large observation identity")
	}
	// 之后 ≥ quantum 的非工具增长（低于下一 T1 level）。
	growCumulativeBy(conversation, int(tracker.lastAutoCheckpointBytes), int(toolOutputPressureBytes))
	if conversation.CumulativeModelVisibleBytes() >= int(contextPressureStepBytes) {
		t.Fatalf("test precondition lost: non-tool growth crossed the T1 level")
	}
	if fire, contextPressure := tracker.shouldAutoCheckpoint(conversation); fire || contextPressure {
		t.Fatalf("forced-checkpoint-covered observation fired T2 after unrelated growth (fire=%v context=%v)", fire, contextPressure)
	}
	// 新的大观察仍可触发。
	appendPendingToolObservation(conversation, strings.Repeat("z", toolBlockContentFor(conversation, int(toolOutputPressureBytes))))
	if fire, _ := tracker.shouldAutoCheckpoint(conversation); !fire {
		t.Fatal("new large observation after the forced checkpoint did not fire")
	}
}

// TestThresholdDecision_GuardsAndRestartReset 证明 no-op 守卫与 restart/续跑后
// 的确定性重建语义。注意：trigger 的 level/字节水位/consumed 水位是进程本地
// 状态，restart 后并不恢复 crash 前的中间代际状态——fresh tracker 从首级与零
// 水位开始，conversation 从 durable checkpoint 重建的新 bootstrap 重新计数。
// 因此决策“给定重建内容确定性地重复”，而不是“与从未 restart 的进程全局一致”
// （该进程会保留旧累计与已推进的 level）。已 checkpoint 的内容在 restart 后
// 不会重复触发，因为重建对话从更小的 reconstruction bootstrap 重新开始，且
// restart 前已达 threshold 的 checkpoint 已经持久化。
func TestThresholdDecision_GuardsAndRestartReset(t *testing.T) {
	conversation := NewAgentConversation(strings.Repeat("b", 1000))
	if fire, contextPressure := (*resilientRunState)(nil).shouldAutoCheckpoint(conversation); fire || contextPressure {
		t.Fatalf("nil tracker fired (fire=%v context=%v)", fire, contextPressure)
	}
	if fire, _ := thresholdDecisionTracker().shouldAutoCheckpoint(nil); fire {
		t.Fatal("nil conversation fired")
	}
	noStore := newResilientRunState(nil, domain.Run{}, "")
	if fire, _ := noStore.shouldAutoCheckpoint(conversation); fire {
		t.Fatal("tracker without checkpoint store fired")
	}

	// restart/续跑确定性（进程本地重置）：fresh tracker 的 T1 level 回到首级，
	// 字节水位与 consumed 水位回到 0。
	restarted := thresholdDecisionTracker()
	if restarted.thresholdCheckpointLevelBytes != contextPressureStepBytes {
		t.Fatalf("restarted level = %d, want first level %d", restarted.thresholdCheckpointLevelBytes, contextPressureStepBytes)
	}
	if restarted.lastAutoCheckpointBytes != 0 || restarted.consumedToolObservationSequence != 0 {
		t.Fatalf("restarted watermarks = %d/%d, want 0/0", restarted.lastAutoCheckpointBytes, restarted.consumedToolObservationSequence)
	}
	// 同一重建内容（fresh conversation）在 fresh tracker 上得到与首轮一致的
	// 决策：不持久化字节水位仍然确定性可重建（用于从 durable checkpoint 重建
	// 的对话，而非声称恢复 crash 前的中间代际状态）。
	first := NewAgentConversation("")
	appendExactToolBlock(t, first, int(contextPressureStepBytes))
	fire, contextPressure := restarted.shouldAutoCheckpoint(first)
	if !fire || !contextPressure {
		t.Fatalf("reconstructed run lost the threshold decision (fire=%v context=%v)", fire, contextPressure)
	}
	second := NewAgentConversation("")
	appendExactToolBlock(t, second, int(contextPressureStepBytes))
	fire2, contextPressure2 := thresholdDecisionTracker().shouldAutoCheckpoint(second)
	if fire != fire2 || contextPressure != contextPressure2 {
		t.Fatalf("identical reconstruction produced different decisions")
	}
}
