package gls

// though this could probably be better at keeping ids smaller, the goal of
// this class is to keep a registry of the smallest unique integer ids
// per-process possible

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	maxIDGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "gls_id_pool_max_id",
		Help: "The current maximum ID in the ID pool",
	})
)

func init() {
	prometheus.MustRegister(maxIDGauge)
}

type idPool struct {
	mtx      sync.Mutex
	released []uint
	max_id   uint
}

func (p *idPool) Acquire() (id uint) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	if len(p.released) > 0 {
		id = p.released[len(p.released)-1]
		p.released = p.released[:len(p.released)-1]
		return id
	}
	id = p.max_id
	p.max_id++
	maxIDGauge.Set(float64(p.max_id))
	return id
}

func (p *idPool) Release(id uint) {
	p.mtx.Lock()
	defer p.mtx.Unlock()
	p.released = append(p.released, id)
}
