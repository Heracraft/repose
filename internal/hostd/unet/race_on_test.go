//go:build race

package unet

// raceOn: the race detector slows every frame several times over, so the
// timing tests (rates, latencies) skip under it and run without it.
const raceOn = true
