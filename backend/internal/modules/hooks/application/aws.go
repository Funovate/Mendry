package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidAWSSNS   = errors.New("invalid AWS SNS message")
	ErrForbiddenAWSSNS = errors.New("forbidden AWS SNS message")
	ErrTemporaryAWSSNS = errors.New("temporary AWS SNS verification failure")
)

// AWSMessageType 是验证后的 SNS 消息类别；control message 不得进入 incident 流程。
type AWSMessageType string

const (
	AWSMessageNotification             AWSMessageType = "Notification"
	AWSMessageSubscriptionConfirmation AWSMessageType = "SubscriptionConfirmation"
	AWSMessageUnsubscribeConfirmation  AWSMessageType = "UnsubscribeConfirmation"
)

// AWSCloudWatchAlarm 是验证后的 CloudWatch 公共告警投影。
type AWSCloudWatchAlarm struct {
	Name           string
	ARN            string
	State          string
	Reason         string
	StateChangedAt time.Time
	Region         string
	AccountID      string
	Description    string
	AlarmRule      string
	Trigger        json.RawMessage
	LogGroupName   string
}

// VerifiedAWSMessage 只保留业务处理需要的已签名 SNS metadata 和 CloudWatch 投影。
type VerifiedAWSMessage struct {
	Type         AWSMessageType
	MessageID    string
	TopicARN     string
	SNSTimestamp time.Time
	Alarm        *AWSCloudWatchAlarm
}

// AWSSNSVerifier 验证 SNS envelope、允许的 Topic、签名和 control URL；
// SubscriptionConfirmation 只有在 bounded GET 成功后才返回。
type AWSSNSVerifier interface {
	Verify(context.Context, string, string) (VerifiedAWSMessage, error)
}
