package main

import "testing"

func TestResolveRecordsWhatItCovers(t *testing.T) {
	for _, behaviour := range []string{
		"resolve records each item a state finding lists, with the size of each file, and resolving again replaces the entry",
		"usage drops entries cleared by a use from resolved.json; serve only reads them",
	} {
		t.Run(behaviour, func(t *testing.T) { pending(t, behaviour) })
	}
}
