package moodle

import (
	"math/rand"
	"time"
)

var seeded = false

func RandomString(size int) string {
	if !seeded {
		seeded = true
		rand.Seed(time.Now().UTC().UnixNano())
	}
	bytes := make([]byte, size)
	for i := 0; i < size; i++ {
		b := uint8(rand.Int31n(36))
		if b < 26 {
			bytes[i] = 'A' + b
		} else if b < 52 {
			bytes[i] = 'a' + (b - 26)
		} else {
			bytes[i] = '0' + (b - 52)
		}
	}
	return string(bytes)
}
