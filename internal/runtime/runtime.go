// Package runtime scales ephemeral workers to the work in a queue.
package runtime

import (
	"context"
)

// Worker is one running worker, as the [Runtime] names it.
type Worker struct {
	ID   string
	Name string
}

// Stats is what a [Runtime] reports about its workers.
type Stats struct {
	// Workers are the workers running now.
	Workers []Worker
}

// Runtime runs workers: containers, cloud jobs, or processes.
type Runtime interface {
	Stats(context.Context) (Stats, error)
	Launch(context.Context) (Worker, error)
	Shutdown(context.Context, Worker) error
}

// QueueStats is what a [Queue] reports about its messages.
type QueueStats struct {
	// Visible counts the messages awaiting a worker.
	Visible int64

	// Pending counts the messages being processed.
	Pending int64
}

// Queue is the work the workers are scaled to.
type Queue interface {
	Stats(context.Context) (QueueStats, error)
}
