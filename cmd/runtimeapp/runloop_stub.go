//go:build !ios || !cgo

package main

func runWithMainLoop(serve func()) { serve() }
