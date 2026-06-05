package notification

const (
	TopicTxCompleted  = "tx.completed"
	TopicTxFailed     = "tx.failed"
	TopicUserVerified = "user.verified"
	TopicUserRejected = "user.rejected"
)

type Event struct {
	Topic   string
	Payload []byte
}

type Message struct {
	ID          string
	AggregateID string
	TraceID     string
	Subject     string
	Body        string
}

func IsSupportedTopic(topic string) bool {
	switch topic {
	case TopicTxCompleted, TopicTxFailed, TopicUserVerified, TopicUserRejected:
		return true
	default:
		return false
	}
}
