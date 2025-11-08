package main

import (
	"crypto/md5"
	"fmt"
	"math/rand"
	"time"
)

func main() {
	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewSource(seed))

	fmt.Printf("key: %s", generateKey(rng))
}

func generateKey(rng *rand.Rand) string {
	minLength := 32
	maxLength := 64
	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	length := rng.Intn(maxLength-minLength) + minLength
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	hash := fmt.Sprintf("%x\n", md5.Sum(b))
	return hash
}
