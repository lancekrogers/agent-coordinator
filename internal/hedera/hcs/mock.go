package hcs

import (
	"context"
	"sync"
	"sync/atomic"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"
)

// MockPublisher is an in-memory HCS publisher for demo mode.
// Messages are stored per-topic and forwarded to any active subscribers.
type MockPublisher struct {
	mu          sync.RWMutex
	topics      map[string][]Envelope
	subscribers map[string][]chan Envelope
	seqCounter  atomic.Uint64
}

// NewMockPublisher creates a new in-memory HCS publisher.
func NewMockPublisher() *MockPublisher {
	return &MockPublisher{
		topics:      make(map[string][]Envelope),
		subscribers: make(map[string][]chan Envelope),
	}
}

// Publish stores a message in memory and forwards it to any active subscribers.
func (m *MockPublisher) Publish(ctx context.Context, topicID hiero.TopicID, msg Envelope) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	key := topicID.String()
	seq := m.seqCounter.Add(1)
	msg.SequenceNum = seq

	m.mu.Lock()
	m.topics[key] = append(m.topics[key], msg)
	subs := make([]chan Envelope, len(m.subscribers[key]))
	copy(subs, m.subscribers[key])
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
			// Subscriber too slow, skip
		}
	}

	return nil
}

// MockSubscriber is an in-memory HCS subscriber for demo mode.
// It shares state with MockPublisher to receive published messages.
type MockSubscriber struct {
	publisher *MockPublisher
}

// NewMockSubscriber creates a subscriber backed by a MockPublisher.
func NewMockSubscriber(publisher *MockPublisher) *MockSubscriber {
	return &MockSubscriber{publisher: publisher}
}

// Subscribe returns a channel that receives messages published to the given topic.
// Existing messages are replayed first, then new messages arrive as they're published.
func (m *MockSubscriber) Subscribe(ctx context.Context, topicID hiero.TopicID) (<-chan Envelope, <-chan error) {
	msgs := make(chan Envelope, 256)
	errs := make(chan error, 1)
	key := topicID.String()

	m.publisher.mu.Lock()
	existing := make([]Envelope, len(m.publisher.topics[key]))
	copy(existing, m.publisher.topics[key])
	m.publisher.subscribers[key] = append(m.publisher.subscribers[key], msgs)
	m.publisher.mu.Unlock()

	go func() {
		defer close(msgs)
		defer close(errs)

		// Replay existing messages
		for _, msg := range existing {
			select {
			case msgs <- msg:
			case <-ctx.Done():
				return
			}
		}

		// Block until context is cancelled — new messages arrive via the publisher
		<-ctx.Done()
	}()

	return msgs, errs
}

// MockTopicCreator is an in-memory topic creator for demo mode.
type MockTopicCreator struct {
	mu       sync.Mutex
	topics   map[string]*TopicMetadata
	nextShard int64
	nextRealm int64
	nextNum   int64
}

// NewMockTopicCreator creates a new in-memory topic creator.
func NewMockTopicCreator() *MockTopicCreator {
	return &MockTopicCreator{
		topics:  make(map[string]*TopicMetadata),
		nextNum: 9000000,
	}
}

// CreateTopic creates a fake topic ID and stores metadata.
func (m *MockTopicCreator) CreateTopic(ctx context.Context, memo string) (hiero.TopicID, error) {
	if ctx.Err() != nil {
		return hiero.TopicID{}, ctx.Err()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.nextNum++
	topicID := hiero.TopicID{
		Shard: 0,
		Realm: 0,
		Topic: uint64(m.nextNum),
	}

	m.topics[topicID.String()] = &TopicMetadata{
		TopicID: topicID,
		Memo:    memo,
	}

	return topicID, nil
}

// DeleteTopic removes a topic from the mock store.
func (m *MockTopicCreator) DeleteTopic(ctx context.Context, topicID hiero.TopicID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.topics, topicID.String())
	return nil
}

// TopicInfo returns metadata for a mock topic.
func (m *MockTopicCreator) TopicInfo(ctx context.Context, topicID hiero.TopicID) (*TopicMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, ok := m.topics[topicID.String()]
	if !ok {
		return &TopicMetadata{TopicID: topicID, Memo: "mock-topic"}, nil
	}
	return meta, nil
}
