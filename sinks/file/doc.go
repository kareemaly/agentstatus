// Package file provides a sink that appends agentstatus Events as JSON Lines
// to files on disk, selecting the destination per-event by expanding a path
// template. The sink buffers events onto an internal channel and drains them
// on a single background goroutine, so slow I/O never blocks the Hub.
//
// See the package-level README for a worked "capture events in the wild"
// example.
package file
