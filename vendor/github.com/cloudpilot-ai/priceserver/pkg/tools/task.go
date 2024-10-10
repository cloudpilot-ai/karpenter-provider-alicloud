package tools

import (
	"context"
	"sync"

	"k8s.io/client-go/util/workqueue"
)

type HandlerFunc func(paras ...interface{})

type ParallelTask struct {
	items       [][]interface{}
	handlerFunc HandlerFunc
}

func NewParallelTask(handlerFunc HandlerFunc) *ParallelTask {
	return &ParallelTask{
		items:       make([][]interface{}, 0),
		handlerFunc: handlerFunc,
	}
}

func (p *ParallelTask) Add(params []interface{}) {
	p.items = append(p.items, params)
}

func (p *ParallelTask) Process() {
	var wg sync.WaitGroup

	wg.Add(len(p.items))
	parallelFunc := func(piece int) {
		defer wg.Done()
		p.handlerFunc(p.items[piece]...)
	}

	workqueue.ParallelizeUntil(context.Background(), 50, len(p.items), parallelFunc)
	wg.Wait()
}
