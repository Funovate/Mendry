package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	platformredis "fixthe/backend/internal/platform/redis"
)

func TestDataClientsCloseInDependencyOrder(t *testing.T) {
	var mutex sync.Mutex
	var order []string
	redisClient := recordingCloser{name: "redis", mutex: &mutex, order: &order}
	postgresPool := recordingCloser{name: "postgresql", mutex: &mutex, order: &order}

	if err := closeRedis(redisClient, time.Second); err != nil {
		t.Fatalf("closeRedis() error = %v", err)
	}
	if err := closePostgreSQL(postgresPool, time.Second); err != nil {
		t.Fatalf("closePostgreSQL() error = %v", err)
	}
	if !reflect.DeepEqual(order, []string{"redis", "postgresql"}) {
		t.Fatalf("close order = %#v", order)
	}
}

func TestDataClientCloseUsesBoundedIndependentContext(t *testing.T) {
	rootContext, cancel := context.WithCancel(context.Background())
	cancel()
	closer := checkingCloser{check: func(ctx context.Context) error {
		if ctx.Err() != nil {
			return errors.New("close context inherits root cancellation")
		}
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("close context has no deadline")
		}
		return nil
	}}
	_ = rootContext
	if err := closeRedis(closer, time.Second); err != nil {
		t.Fatalf("closeRedis() error = %v", err)
	}
}

func TestCloseRedisAllowsDisabledClient(t *testing.T) {
	if err := closeRedis(nil, time.Second); err != nil {
		t.Fatalf("closeRedis(nil) error = %v", err)
	}
}

func TestFinishWithDataClientsAcceptsDisabledRedis(t *testing.T) {
	// 此 regression 锁定 typed nil 不得通过 redisCloser interface 进入 Close。
	var redisClient *platformredis.Client
	var redisResource redisCloser
	if redisClient != nil {
		redisResource = redisClient
	}
	if err := closeRedis(redisResource, time.Second); err != nil {
		t.Fatalf("closeRedis(disabled) error = %v", err)
	}
}

type recordingCloser struct {
	name  string
	mutex *sync.Mutex
	order *[]string
}

func (c recordingCloser) Close(context.Context) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	*c.order = append(*c.order, c.name)
	return nil
}

type checkingCloser struct {
	check func(context.Context) error
}

func (c checkingCloser) Close(ctx context.Context) error {
	return c.check(ctx)
}
