package channels

// Transport is the platform-neutral lifecycle and delivery contract shared by
// every communication channel. Gateway binding remains in the composition
// layer so this package does not depend on the main application package.
type Transport interface {
	Name() string
	Type() string
	Start() error
	Stop() error
	SendMessage(recipient string, content string) error
}
