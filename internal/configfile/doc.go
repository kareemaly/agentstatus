// Package configfile contains internal helpers for safely reading and
// writing adapter configuration files: atomic write via temp-file-and-
// rename, timestamped .bak backups, and cross-process flock(2).
package configfile
