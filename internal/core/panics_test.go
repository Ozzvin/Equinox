package core

import "testing"

// A panic in a goroutine of ours that the program can live without is written down, not the end of the program.
func TestSurviveKeepsTheProgramGoing(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer survive("a test")
		panic("boom")
	}()
	<-done
}
