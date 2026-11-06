// Package data reads Tarantool journals - snapshots (.snap), write-ahead logs
// (.xlog) and vinyl metadata logs (.vylog) - without Tarantool, and is the
// implementation of tt data cat and tt data play.
//
// [Collect] turns the files and directories of the command line into the
// journal files to read, in the order of their vclocks, and [Select] picks
// what of them a [Filter] asks for: at most one snapshot, the newest within
// the bounds, and the write-ahead log rows it does not already contain.
// [Scan] reads the selection transaction by transaction; a transaction is
// never cut by a bound. [Cat] prints the transactions, [Play] applies them to
// an instance over IPROTO.
package data
