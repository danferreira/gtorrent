package peer

import (
	"sync"
)

type Pool struct {
	mu   sync.Mutex
	q    []Peer // ring buffer
	seen map[string]struct{}
}

func NewPool(cap int) *Pool {
	return &Pool{q: make([]Peer, 0, cap), seen: make(map[string]struct{})}
}

func (p *Pool) PushMany(list []Peer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pr := range list {
		key := pr.Addr
		if _, ok := p.seen[key]; ok {
			continue
		}
		p.seen[key] = struct{}{}
		p.q = append(p.q, pr)
	}
}

func (p *Pool) Pop() (Peer, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.q) == 0 {
		return Peer{}, false
	}
	pr := p.q[0]
	p.q = p.q[1:]
	return pr, true
}

func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.q)
}
