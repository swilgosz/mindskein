package usage

import "testing"

func pending(t *testing.T, behaviour string) {
	t.Helper()
	t.Fatalf("PENDING: %s", behaviour)
}

func TestResolvedRatesAndStates(t *testing.T) {
	for _, behaviour := range []string{
		"resolve context-bloat hides it until sessions started after the resolve time run above the threshold share again",
		"cache-writes and skill-listing follow the same rate rule",
		"a resolved unused skill stays hidden while unused; using it clears the entry, and a later unused stretch is reported as new",
		"resolving unused-skills as a whole hides the skills unused now; a skill that becomes unused later is reported",
		"a resolved instruction file is reported again once it grows more than 10% past its size at resolve time; a memory index likewise by lines",
	} {
		t.Run(behaviour, func(t *testing.T) { pending(t, behaviour) })
	}
}
