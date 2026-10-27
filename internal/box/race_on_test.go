//go:build race

package box

// raceBuild says these tests were built with the race detector. A program
// a test builds is then built with it too, so it shares what the tests'
// own build already compiled instead of compiling everything a second time.
const raceBuild = true
