package hcs

import (
	"context"
	"fmt"
	"time"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"
)

const (
	defaultMessageBuffer  = 100
	defaultReconnectDelay = 2 * time.Second
	defaultMaxReconnects  = 10
)

// SubscribeConfig holds configuration for the subscriber.
type SubscribeConfig struct {
	MessageBuffer  int
	ReconnectDelay time.Duration
	MaxReconnects  int
}

// DefaultSubscribeConfig returns sensible defaults for testnet usage.
func DefaultSubscribeConfig() SubscribeConfig {
	return SubscribeConfig{
		MessageBuffer:  defaultMessageBuffer,
		ReconnectDelay: defaultReconnectDelay,
		MaxReconnects:  defaultMaxReconnects,
	}
}

// Subscriber implements the MessageSubscriber interface using the Hiero (Hedera) SDK.
type Subscriber struct {
	client *hiero.Client
	config SubscribeConfig
}

// NewSubscriber creates a new Subscriber with the given Hiero client and config.
func NewSubscriber(client *hiero.Client, config SubscribeConfig) *Subscriber {
	return &Subscriber{
		client: client,
		config: config,
	}
}

// Subscribe starts a streaming subscription to an HCS topic.
// Messages are delivered to the returned channel. The subscription
// runs until the context is cancelled.
func (s *Subscriber) Subscribe(ctx context.Context, topicID hiero.TopicID) (<-chan Envelope, <-chan error) {
	msgCh := make(chan Envelope, s.config.MessageBuffer)
	errCh := make(chan error, s.config.MessageBuffer)

	go s.runSubscription(ctx, topicID, msgCh, errCh)

	return msgCh, errCh
}

func (s *Subscriber) runSubscription(
	ctx context.Context,
	topicID hiero.TopicID,
	msgCh chan<- Envelope,
	errCh chan<- error,
) {
	defer close(msgCh)
	defer close(errCh)

	for reconnects := 0; reconnects <= s.config.MaxReconnects; reconnects++ {
		if ctx.Err() != nil {
			return
		}

		err := s.subscribeOnce(ctx, topicID, msgCh, errCh)
		if err == nil || ctx.Err() != nil {
			return
		}

		select {
		case errCh <- fmt.Errorf("subscribe to topic %s attempt %d: %w", topicID, reconnects+1, err):
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.config.ReconnectDelay):
		}
	}

	select {
	case errCh <- fmt.Errorf("subscribe to topic %s: exhausted %d reconnect attempts", topicID, s.config.MaxReconnects+1):
	default:
	}
}

func (s *Subscriber) subscribeOnce(
	ctx context.Context,
	topicID hiero.TopicID,
	msgCh chan<- Envelope,
	errCh chan<- error,
) error {
	handle, err := hiero.NewTopicMessageQuery().
		SetTopicID(topicID).
		SetStartTime(time.Now().Add(-30 * time.Second)).
		Subscribe(s.client, func(message hiero.TopicMessage) {
			env, err := UnmarshalEnvelope(message.Contents)
			if err != nil {
				select {
				case errCh <- fmt.Errorf("deserialize from topic %s seq %d: %w",
					topicID, message.SequenceNumber, err):
				default:
				}
				return
			}

			select {
			case msgCh <- *env:
			case <-ctx.Done():
			}
		})
	if err != nil {
		return fmt.Errorf("start subscription: %w", err)
	}

	<-ctx.Done()
	handle.Unsubscribe()
	return nil
}

// Compile-time interface compliance check.
var _ MessageSubscriber = (*Subscriber)(nil)
