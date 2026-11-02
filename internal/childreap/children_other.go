//go:build !linux && !darwin

package childreap

func children(int) []int { return nil }
