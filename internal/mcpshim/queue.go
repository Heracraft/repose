package mcpshim

import "sync"

// MaxQueued is the most bytes a Queue holds for a writer that has stopped
// reading. Past it the stream ends rather than stalling the others.
const MaxQueued = 16 << 20

// Queue carries byte slices to one writer, in order, without ever blocking
// whoever pushes: hold's frame reader and the laptop end each serve every
// stream from one loop, so one agent or server that stops reading must not
// hold up the rest, pings included.
type Queue struct {
	mu     sync.Mutex
	bufs   [][]byte
	size   int
	closed bool
	ready  chan struct{}
}

// NewQueue returns an empty, open queue.
func NewQueue() *Queue { return &Queue{ready: make(chan struct{}, 1)} }

func (q *Queue) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// Push queues b. It returns false when the queue is closed, or when b
// would take it past MaxQueued, in which case it also aborts the queue.
func (q *Queue) Push(b []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if q.size+len(b) > MaxQueued {
		q.closed, q.bufs, q.size = true, nil, 0
		q.signal()
		return false
	}
	q.bufs = append(q.bufs, b)
	q.size += len(b)
	q.signal()
	return true
}

// Close takes no more; Next still returns what is queued.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.signal()
}

// Abort takes no more and drops what is queued.
func (q *Queue) Abort() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed, q.bufs, q.size = true, nil, 0
	q.signal()
}

// Next waits for the next slice. It returns false once the queue is closed
// and empty.
func (q *Queue) Next() ([]byte, bool) {
	for {
		q.mu.Lock()
		if len(q.bufs) > 0 {
			b := q.bufs[0]
			q.bufs[0] = nil
			q.bufs = q.bufs[1:]
			q.size -= len(b)
			q.mu.Unlock()
			return b, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, false
		}
		<-q.ready
	}
}
