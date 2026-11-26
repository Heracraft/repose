package unet

import (
	"encoding/binary"
	"math"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// txItem is one frame waiting to go to a guest. v, when set, owns b and
// is released once b is written: a frame from the stack is written from
// the stack's own view, with no copy of ours.
type txItem struct {
	b  []byte
	v  *buffer.View
	at time.Time
}

func (it txItem) release() {
	if it.v != nil {
		it.v.Release()
	}
}

// The fair queue is fq_codel's shape (RFC 8290) at the size of one guest:
// frames are hashed by flow into fqFlows queues served by deficit round
// robin with fqQuantum bytes a turn; a flow that was empty goes on the
// new list and is served before the old ones, so an ssh keystroke does
// not wait behind a download's backlog; and each queue runs CoDel, which
// drops at the head once its frames have waited over codelTarget for a
// codelInterval, so the backlog itself stays short. Today's Linux shape
// is the same idea: fq_codel under the HTB class (DECISIONS I-451).
const (
	fqFlows       = 1024
	fqQuantum     = 1514
	codelTarget   = 5 * time.Millisecond
	codelInterval = 100 * time.Millisecond
)

type flowQ struct {
	items   []txItem
	head    int
	bytes   int
	deficit int
	list    int // 0 none, 1 new, 2 old

	dropping   bool
	firstAbove time.Time
	dropNext   time.Time
	count      int
	lastCount  int
}

func (f *flowQ) len() int { return len(f.items) - f.head }

func (f *flowQ) push(it txItem) {
	if f.head > 0 && f.head == len(f.items) {
		f.items, f.head = f.items[:0], 0
	}
	f.items = append(f.items, it)
	f.bytes += len(it.b)
}

func (f *flowQ) pop() (txItem, bool) {
	if f.len() == 0 {
		return txItem{}, false
	}
	it := f.items[f.head]
	f.items[f.head] = txItem{}
	f.head++
	f.bytes -= len(it.b)
	return it, true
}

// fairQueue is a guest's download queue.
type fairQueue struct {
	flows    [fqFlows]*flowQ
	newL     []int
	oldL     []int
	bytes    int64
	dropped  uint64 // frames CoDel or the byte bound dropped
	codelOff bool   // PlainFIFO: no CoDel
	fifo     bool   // PlainFIFO: one flow
}

// flowHash reads the frame's IPv4 addresses, protocol and ports.
func flowHash(frame []byte) int {
	if len(frame) < header.EthernetMinimumSize+header.IPv4MinimumSize || header.Ethernet(frame).Type() != header.IPv4ProtocolNumber {
		return 0
	}
	ip := frame[header.EthernetMinimumSize:]
	h := uint32(2166136261)
	mix := func(b []byte) {
		for _, c := range b {
			h ^= uint32(c)
			h *= 16777619
		}
	}
	mix(ip[12:20]) // src, dst
	mix(ip[9:10])  // protocol
	ihl := int(ip[0]&0x0f) * 4
	if (ip[9] == 6 || ip[9] == 17) && len(ip) >= ihl+4 && binary.BigEndian.Uint16(ip[6:8])&0x1fff == 0 {
		mix(ip[ihl : ihl+4]) // ports
	}
	return int(h % fqFlows)
}

// enqueue adds a frame; over limit bytes, the head of the fattest flow is
// dropped (fq_codel's overflow rule), which is the bulk flow, never the
// keystroke.
func (q *fairQueue) enqueue(it txItem, limit int64) {
	i := 0
	if !q.fifo {
		i = flowHash(it.b)
	}
	f := q.flows[i]
	if f == nil {
		f = &flowQ{}
		q.flows[i] = f
	}
	f.push(it)
	q.bytes += int64(len(it.b))
	if f.list == 0 {
		f.list, f.deficit = 1, fqQuantum
		q.newL = append(q.newL, i)
	}
	for q.bytes > limit {
		fat := q.fattest()
		if fat == nil {
			break
		}
		d, _ := fat.pop()
		q.bytes -= int64(len(d.b))
		q.dropped++
		d.release()
	}
}

func (q *fairQueue) fattest() *flowQ {
	var best *flowQ
	for _, l := range [][]int{q.newL, q.oldL} {
		for _, i := range l {
			if f := q.flows[i]; f.len() > 0 && (best == nil || f.bytes > best.bytes) {
				best = f
			}
		}
	}
	return best
}

// dequeue picks the next frame by DRR, new flows first, through CoDel.
func (q *fairQueue) dequeue(now time.Time) (txItem, bool) {
	for {
		var l *[]int
		switch {
		case len(q.newL) > 0:
			l = &q.newL
		case len(q.oldL) > 0:
			l = &q.oldL
		default:
			return txItem{}, false
		}
		i := (*l)[0]
		f := q.flows[i]
		if f.deficit <= 0 {
			f.deficit += fqQuantum
			*l = (*l)[1:]
			f.list = 2
			q.oldL = append(q.oldL, i)
			continue
		}
		it, ok := q.codelPop(f, now)
		if !ok {
			*l = (*l)[1:]
			if l == &q.newL {
				// An emptied new flow moves to the old list, so a flow
				// cannot stay "new" by sending one frame at a time.
				f.list = 2
				q.oldL = append(q.oldL, i)
			} else {
				f.list = 0
			}
			continue
		}
		f.deficit -= len(it.b)
		return it, true
	}
}

func (q *fairQueue) take(f *flowQ) (txItem, bool) {
	it, ok := f.pop()
	if ok {
		q.bytes -= int64(len(it.b))
	}
	return it, ok
}

func controlLaw(t time.Time, count int) time.Time {
	return t.Add(time.Duration(float64(codelInterval) / math.Sqrt(float64(count))))
}

// okToDrop is CoDel's test on the frame just taken.
func (f *flowQ) okToDrop(it txItem, now time.Time) bool {
	if now.Sub(it.at) < codelTarget || f.bytes < fqQuantum {
		f.firstAbove = time.Time{}
		return false
	}
	if f.firstAbove.IsZero() {
		f.firstAbove = now.Add(codelInterval)
		return false
	}
	return !now.Before(f.firstAbove)
}

// codelPop is RFC 8289's dequeue for one flow.
func (q *fairQueue) codelPop(f *flowQ, now time.Time) (txItem, bool) {
	it, ok := q.take(f)
	if !ok {
		f.dropping = false
		return txItem{}, false
	}
	if q.codelOff {
		return it, true
	}
	drop := f.okToDrop(it, now)
	if f.dropping {
		if !drop {
			f.dropping = false
			return it, true
		}
		for !now.Before(f.dropNext) && f.dropping {
			it.release()
			q.dropped++
			f.count++
			if it, ok = q.take(f); !ok {
				f.dropping = false
				return txItem{}, false
			}
			if !f.okToDrop(it, now) {
				f.dropping = false
			} else {
				f.dropNext = controlLaw(f.dropNext, f.count)
			}
		}
		return it, true
	}
	if drop {
		it.release()
		q.dropped++
		if it, ok = q.take(f); !ok {
			return txItem{}, false
		}
		f.dropping = true
		delta := f.count - f.lastCount
		if delta > 1 && now.Sub(f.dropNext) < 16*codelInterval {
			f.count = delta
		} else {
			f.count = 1
		}
		f.lastCount = f.count
		f.dropNext = controlLaw(now, f.count)
	}
	return it, true
}

// txQueue is a port's transmit side: the free FIFO (ShapeExempt sources,
// ARP), served first, and the fair queue the shaper drains.
type txQueue struct {
	mu    sync.Mutex
	free  []txItem
	fq    fairQueue
	wake  chan struct{}
	limit func() int64
}

func newTxQueue(fifo bool, limit func() int64) *txQueue {
	t := &txQueue{wake: make(chan struct{}, 1), limit: limit}
	t.fq.fifo, t.fq.codelOff = fifo, fifo
	return t
}

func (t *txQueue) put(it txItem, shaped bool) {
	t.mu.Lock()
	if shaped {
		t.fq.enqueue(it, t.limit())
	} else if len(t.free) < queueFrames {
		t.free = append(t.free, it)
	} else {
		it.release() // full: dropped, as a full qdisc drops
	}
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// next returns the next frame, free ones first; shaped says which.
func (t *txQueue) next(now time.Time) (txItem, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.free) > 0 {
		it := t.free[0]
		t.free[0] = txItem{}
		t.free = t.free[1:]
		return it, false, true
	}
	it, ok := t.fq.dequeue(now)
	return it, true, ok
}

func (t *txQueue) drain() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, it := range t.free {
		it.release()
	}
	t.free = nil
	for {
		it, ok := t.fq.dequeue(time.Now())
		if !ok {
			return
		}
		it.release()
	}
}
