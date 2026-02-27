package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

var idSeq uint64

func nextID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), atomic.AddUint64(&idSeq, 1))
}
