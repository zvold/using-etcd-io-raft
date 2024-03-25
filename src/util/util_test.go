package util

import (
	"math/rand"
	"testing"
	"time"
)

func Test_Conversion(t *testing.T) {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < 1000; i++ {
		x := r.Int()
		bytes := IntToBytes(x)
		x2 := BytesToInt(bytes)
		if x != x2 {
			t.Fatalf("Back-and-forth conversion of x = %x fails.", x)
		}
	}
}
