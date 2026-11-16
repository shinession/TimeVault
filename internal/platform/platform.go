// Package platform isolates the operating-system specific calls TimeVault
// needs (Windows file attributes, disk space, lock detection, process
// priority) so the rest of the code, and its tests, stay portable.
package platform
