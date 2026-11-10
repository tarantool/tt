// Package fetch downloads files over HTTP for tt and its modules: every
// request with a timeout, every body with a bound on its size, and a file
// whose SHA-256 is known in advance checked against it and kept in a cache.
//
// [Client] makes the requests. [Client.Get] returns the reply for a caller
// that reads it itself; [Client.Copy] and [Client.Bytes] take the body of a
// 200 OK reply, at most [Client.Limit] bytes of it. A reply other than 200 OK
// is an [*HTTPError], which wraps [ErrHTTPStatus], and a body above the limit
// wraps [ErrTooLarge]. The errors of Copy and Bytes name the URL.
//
// [Fetcher] downloads a file of a known SHA-256 through a Client and checks
// it, a mismatch wrapping [ErrChecksum]. With a cache directory it keeps the
// file there, named by its SHA-256: a cached copy is used, with no request,
// only when its own SHA-256 is the one asked for, and failing to read or to
// fill the cache fails nothing.
//
// [UnpackFile] takes one regular file out of a .tar.gz archive in memory, as
// a release archive holds a program, and [SHA256] is the digest the Fetcher
// checks, in hex.
//
// The package depends on the standard library only.
package fetch
