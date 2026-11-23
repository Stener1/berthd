//go:build !unix

package childreap

func wait(int) (done, gone bool) { return false, true }
