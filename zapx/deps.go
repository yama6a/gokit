package zapx

import "context"

// Sink stores log entries in an external system such as a database table.
type Sink interface {
	Write(ctx context.Context, e Entry) error
}
