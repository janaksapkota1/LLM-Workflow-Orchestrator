package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"llm-orchestrator/internal/models"
)

const (
	// JobQueue is the primary FIFO queue for step execution.
	JobQueue = "llm:jobs"
	// RetryQueue is a sorted set used for delayed retries (score = execute-after unix ts).
	RetryQueue = "llm:jobs:retry"
	// DeadLetterQueue holds jobs that exhausted all retries.
	DeadLetterQueue = "llm:jobs:dead"
)

// Queue wraps a Redis client to provide job enqueue/dequeue semantics.
type Queue struct {
	rdb *redis.Client
}

// New creates a Queue backed by the provided Redis client.
func New(rdb *redis.Client) *Queue {
	return &Queue{rdb: rdb}
}

// Connect creates and pings a new Redis client using the given options.
func Connect(ctx context.Context, addr, password string, db int) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return rdb, nil
}

// Enqueue pushes a job to the right end of the main queue (RPUSH).
func (q *Queue) Enqueue(ctx context.Context, job *models.JobMessage) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	return q.rdb.RPush(ctx, JobQueue, payload).Err()
}

// Dequeue blocks up to `timeout` waiting for a job from the left end of the queue (BLPOP).
// Returns nil, nil when timeout elapses with no message.
func (q *Queue) Dequeue(ctx context.Context, timeout time.Duration) (*models.JobMessage, error) {
	result, err := q.rdb.BLPop(ctx, timeout, JobQueue).Result()
	if err == redis.Nil {
		return nil, nil // timeout, no message
	}
	if err != nil {
		return nil, fmt.Errorf("blpop: %w", err)
	}

	// result[0] = key name, result[1] = value
	var job models.JobMessage
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}
	return &job, nil
}

// EnqueueRetry schedules a job to be re-queued after `delay`.
// The job is stored in a sorted set scored by the Unix execution time.
func (q *Queue) EnqueueRetry(ctx context.Context, job *models.JobMessage, delay time.Duration) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal retry job: %w", err)
	}
	score := float64(time.Now().Add(delay).Unix())
	return q.rdb.ZAdd(ctx, RetryQueue, redis.Z{Score: score, Member: string(payload)}).Err()
}

// PromoteDueRetries moves jobs from the retry sorted set whose score (execute-at)
// is <= now into the main queue. Should be called periodically by a background goroutine.
func (q *Queue) PromoteDueRetries(ctx context.Context) (int, error) {
	now := fmt.Sprintf("%d", time.Now().Unix())
	members, err := q.rdb.ZRangeByScore(ctx, RetryQueue, &redis.ZRangeBy{
		Min: "-inf",
		Max: now,
	}).Result()
	if err != nil {
		return 0, fmt.Errorf("zrangebyscore retry: %w", err)
	}

	for _, member := range members {
		pipe := q.rdb.TxPipeline()
		pipe.RPush(ctx, JobQueue, member)
		pipe.ZRem(ctx, RetryQueue, member)
		if _, err := pipe.Exec(ctx); err != nil {
			return 0, fmt.Errorf("promote retry: %w", err)
		}
	}
	return len(members), nil
}

// SendToDeadLetter moves a job to the dead-letter list after exhausted retries.
func (q *Queue) SendToDeadLetter(ctx context.Context, job *models.JobMessage) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return q.rdb.RPush(ctx, DeadLetterQueue, payload).Err()
}

// QueueDepth returns the number of jobs currently in the main queue.
func (q *Queue) QueueDepth(ctx context.Context) (int64, error) {
	return q.rdb.LLen(ctx, JobQueue).Result()
}
