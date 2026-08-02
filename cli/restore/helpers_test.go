package restore

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"
	"github.com/tarantool/go-xlog/writer"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/archive"
)

const (
	// masterUUID is the instance UUID a backup archive carries: the instance
	// the backup was taken on.
	masterUUID = "11111111-1111-1111-1111-111111111111"
	// replicaUUID is a different instance's UUID, used wherever a test needs
	// the stamp to be visible: a restore onto the instance the archive came
	// from would rewrite the headers to what they already say.
	replicaUUID = "22222222-2222-2222-2222-222222222222"
	// replicasetUUID is the replicaset every archive of one chain belongs to.
	replicasetUUID = "33333333-3333-3333-3333-333333333333"

	zstdLevel = 3
)

// mkBody builds a minimal valid msgpack DML body: {IPROTO_TUPLE: [v]}.
func mkBody(v uint64) []byte {
	buf := []byte{0x81, byte(iproto.IPROTO_TUPLE), 0x91}
	if v < 0x80 {
		return append(buf, byte(v))
	}

	return append(buf, 0xcf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func row(replicaID uint32, lsn int64) format.XRow {
	return format.XRow{
		Type:      iproto.IPROTO_INSERT,
		ReplicaID: replicaID,
		LSN:       lsn,
		BodyRaw:   mkBody(uint64(lsn)),
	}
}

// txsOf turns a list of LSNs on one replica into single-row transactions. The
// replica is spelled out at every call site because a journal that carries
// two replicas is what the trim is chosen wrongly on.
//
//nolint:unparam // the replica belongs in the fixture, not in the helper
func txsOf(replicaID uint32, lsns ...int64) [][]format.XRow {
	txs := make([][]format.XRow, 0, len(lsns))
	for _, lsn := range lsns {
		txs = append(txs, []format.XRow{row(replicaID, lsn)})
	}

	return txs
}

// txsMixed turns (replica, lsn) pairs into one single-row transaction each, in
// the order given. txsOf can only describe one replica's writes; a master's
// journal holds the whole replicaset's, interleaved, and the order they were
// written in is the only thing a recovery point can be resolved against.
func txsMixed(keys ...rowKey) [][]format.XRow {
	txs := make([][]format.XRow, 0, len(keys))
	for _, key := range keys {
		txs = append(txs, []format.XRow{row(key.ReplicaID, key.LSN)})
	}

	return txs
}

func meta(t *testing.T, ft format.Filetype, instUUID string, vclock, prev format.VClock,
) *format.Meta {
	t.Helper()

	id, err := uuid.Parse(instUUID)
	require.NoError(t, err)

	return &format.Meta{
		Filetype:     ft,
		Version:      "tt-test/1.0",
		InstanceUUID: id,
		VClock:       vclock,
		PrevVClock:   prev,
	}
}

// writeJournal writes one journal file named after its vclock signature — the
// <signature>.<ext> convention Tarantool and dir.OpenDir both rely on.
func writeJournal(
	t *testing.T, dir string, ft format.Filetype, vclock, prev format.VClock,
	txs [][]format.XRow,
) string {
	t.Helper()

	ext, err := ft.Ext()
	require.NoError(t, err)

	path := filepath.Join(dir, fmt.Sprintf("%020d%s", vclock.Signature(), ext))

	w, err := writer.Create(path, meta(t, ft, masterUUID, vclock, prev))
	require.NoError(t, err)

	for _, tx := range txs {
		require.NoError(t, w.WriteTx(tx))
	}

	require.NoError(t, w.Close())

	return path
}

func writeXlog(t *testing.T, dir string, vclock, prev format.VClock, txs [][]format.XRow) string {
	t.Helper()

	return writeJournal(t, dir, format.FiletypeXLOG, vclock, prev, txs)
}

func writeSnap(t *testing.T, dir string, vclock format.VClock) string {
	t.Helper()

	return writeJournal(t, dir, format.FiletypeSNAP, vclock, nil, nil)
}

// writeFragment writes the per-shard manifest fragment an archive carries.
func writeFragment(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, fragmentEntryName)
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1}`), 0o644))

	return path
}

// fragmentOf describes one archive of a chain the way tt backup start does:
// the instance it was taken on and the journal positions it spans. A full
// backup has no begin position, so only an increment carries one.
func fragmentOf(backupType backup.BackupType, begin, end uint64) backup.Fragment {
	fragment := backup.Fragment{
		ReplicasetUUID: replicasetUUID,
		InstanceUUID:   masterUUID,
		InstanceName:   "instance-001",
		Hostname:       "node-1",
		Type:           backupType,
		VclockEnd:      backup.Vclock{1: end},
	}

	if backupType == backup.BackupTypeIncremental {
		fragment.VclockBegin = backup.Vclock{1: begin}
	}

	return fragment
}

// writeFragmentOf writes a complete manifest fragment, the one a real archive
// carries, as opposed to the stub writeFragment leaves.
func writeFragmentOf(t *testing.T, dir string, fragment backup.Fragment) string {
	t.Helper()

	data, err := json.Marshal(fragment)
	require.NoError(t, err)

	path := filepath.Join(dir, fragmentEntryName)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	return path
}

// describedArchive packs one xlog starting at signature together with the
// fragment describing the backup it was cut from.
func describedArchive(
	t *testing.T, name string, signature int64, lsns []int64, fragment backup.Fragment,
) string {
	t.Helper()

	src := t.TempDir()
	xlog := writeXlog(t, src, format.VClock{1: signature}, nil, txsOf(1, lsns...))

	return packArchive(t, filepath.Join(t.TempDir(), name),
		xlog, writeFragmentOf(t, src, fragment))
}

// describedChain builds a chain whose two archives hold the same journal under
// the same name -- the increment ships the longer copy of the boundary xlog
// and nothing else, which is what a backup taken between two WAL rotations
// looks like. Only the fragments say which of the two continues the other:
//
//	full: 0.xlog holding lsn 1..2, fragment ending at 2
//	inc:  0.xlog holding lsn 1..4, fragment spanning 2..4
func describedChain(t *testing.T) (full, inc string) {
	t.Helper()

	full = describedArchive(t, "full.tar.zst", 0, []int64{1, 2},
		fragmentOf(backup.BackupTypeFull, 0, 2))
	inc = describedArchive(t, "inc.tar.zst", 0, []int64{1, 2, 3, 4},
		fragmentOf(backup.BackupTypeIncremental, 2, 4))

	return full, inc
}

// packArchive packs files into a .tar.zst under dst and returns its path.
func packArchive(t *testing.T, dst string, files ...string) string {
	t.Helper()

	require.NoError(t, archive.Pack(dst, files, zstdLevel))

	return dst
}

// rawEntry is one file packed under an entry name of its own.
type rawEntry struct {
	name string
	path string
}

// packRawArchive packs the entries under the names they carry, which
// archive.Pack cannot do: it stores base names only. Plain `tar -C dir .`
// prefixes every entry with "./", and an archive from anywhere but tt backup
// is under no obligation to be flat.
func packRawArchive(t *testing.T, dst string, entries ...rawEntry) string {
	t.Helper()

	out, err := os.Create(dst)
	require.NoError(t, err)

	zw, err := zstd.NewWriter(out)
	require.NoError(t, err)

	tw := tar.NewWriter(zw)

	for _, entry := range entries {
		body, err := os.ReadFile(entry.path)
		require.NoError(t, err)

		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}))

		_, err = tw.Write(body)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())
	require.NoError(t, out.Close())

	return dst
}

// checksumOf returns the sha256 an archive should be verified against.
func checksumOf(t *testing.T, path string) string {
	t.Helper()

	sum, err := archive.Checksum(path)
	require.NoError(t, err)

	return sum
}

// archiveChain builds a two-archive backup chain of one replicaset:
//
//	full: 0.snap + 0.xlog holding lsn 1..3 on replica 1
//	inc:  3.xlog holding lsn 4..6 on replica 1
//
// It returns the archive paths in apply order.
func archiveChain(t *testing.T) (full, inc string) {
	t.Helper()

	src := t.TempDir()
	out := t.TempDir()

	fullDir := filepath.Join(src, "full")
	require.NoError(t, os.MkdirAll(fullDir, 0o755))
	snap := writeSnap(t, fullDir, format.VClock{1: 0})
	xlog0 := writeXlog(t, fullDir, format.VClock{1: 0}, nil, txsOf(1, 1, 2, 3))

	incDir := filepath.Join(src, "inc")
	require.NoError(t, os.MkdirAll(incDir, 0o755))
	xlog3 := writeXlog(t, incDir, format.VClock{1: 3}, format.VClock{1: 0}, txsOf(1, 4, 5, 6))

	full = packArchive(t, filepath.Join(out, "full.tar.zst"),
		snap, xlog0, writeFragment(t, fullDir))
	inc = packArchive(t, filepath.Join(out, "inc.tar.zst"),
		xlog3, writeFragment(t, incDir))

	return full, inc
}

// chainPastPoint builds a single archive whose content reaches past the point
// a caller will restore to:
//
//	0.snap + 0.xlog holding lsn 1..3
//	3.xlog holding lsn 4..6
//	6.snap + 6.xlog holding lsn 7..9
func chainPastPoint(t *testing.T) string {
	t.Helper()

	src := t.TempDir()

	files := []string{
		writeSnap(t, src, format.VClock{1: 0}),
		writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2, 3)),
		writeXlog(t, src, format.VClock{1: 3}, format.VClock{1: 0}, txsOf(1, 4, 5, 6)),
		writeSnap(t, src, format.VClock{1: 6}),
		writeXlog(t, src, format.VClock{1: 6}, format.VClock{1: 3}, txsOf(1, 7, 8, 9)),
	}

	return packArchive(t, filepath.Join(t.TempDir(), "wide.tar.zst"), files...)
}

// twoReplicaChain packs one archive of a replicaset whose journals carry both
// members' positions -- the normal case on a master, and what every other
// fixture here leaves out. Each file is named after the combined signature, so
// the name matches neither replica's LSN and the two axes disagree about which
// file a given LSN sits in:
//
//	00000000000000000000.snap  {1:0, 2:0}
//	00000000000000000000.xlog  {1:0, 2:0}  r1 1..5 with r2 1 in the middle
//	00000000000000000006.xlog  {1:5, 2:1}  r2 2..6 with r1 6..7 in the middle
//	00000000000000000013.xlog  {1:7, 2:6}  r1 8..9 with r2 7 in the middle
func twoReplicaChain(t *testing.T) string {
	t.Helper()

	src := t.TempDir()

	files := []string{
		writeSnap(t, src, format.VClock{1: 0, 2: 0}),
		writeXlog(t, src, format.VClock{1: 0, 2: 0}, nil, txsMixed(
			rowKey{1, 1}, rowKey{1, 2}, rowKey{2, 1},
			rowKey{1, 3}, rowKey{1, 4}, rowKey{1, 5})),
		writeXlog(t, src, format.VClock{1: 5, 2: 1}, format.VClock{1: 0, 2: 0}, txsMixed(
			rowKey{2, 2}, rowKey{1, 6}, rowKey{2, 3},
			rowKey{2, 4}, rowKey{1, 7}, rowKey{2, 5}, rowKey{2, 6})),
		writeXlog(t, src, format.VClock{1: 7, 2: 6}, format.VClock{1: 5, 2: 1}, txsMixed(
			rowKey{1, 8}, rowKey{2, 7}, rowKey{1, 9})),
	}

	return packArchive(t, filepath.Join(t.TempDir(), "two-replicas.tar.zst"), files...)
}

// compactInstanceUUID rewrites a journal file's Instance line to the 32-char
// hyphenless spelling of the same UUID. It parses and reads back exactly the
// same, but is four bytes shorter than the canonical form, so an in-place
// overwrite would shift every byte behind it.
func compactInstanceUUID(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	compact := strings.ReplaceAll(masterUUID, "-", "")
	patched := strings.Replace(string(raw), "Instance: "+masterUUID, "Instance: "+compact, 1)
	require.NotEqual(t, string(raw), patched, "the Instance line must have been rewritten")

	require.NoError(t, os.WriteFile(path, []byte(patched), 0o644))
}

// writeGarbageXlog writes a file named like a journal but holding no header.
func writeGarbageXlog(t *testing.T, dir string, signature int64) string {
	t.Helper()

	path := filepath.Join(dir, fmt.Sprintf("%020d.xlog", signature))
	require.NoError(t, os.WriteFile(path, []byte("not an xlog at all"), 0o644))

	return path
}

type rowKey struct {
	ReplicaID uint32
	LSN       int64
}

// readRows returns every row's (ReplicaID, LSN) from a journal file.
func readRows(t *testing.T, path string) []rowKey {
	t.Helper()

	r, err := reader.Open(path)
	require.NoError(t, err)

	defer func() { _ = r.Close() }()

	var out []rowKey

	for row, err := range r.Rows() {
		require.NoError(t, err)
		out = append(out, rowKey{ReplicaID: row.ReplicaID, LSN: row.LSN})
	}

	require.True(t, r.SawEOFMarker(), "file %s must end with a valid EOF marker", path)

	return out
}

// readRowBodies returns the payload of every row in a journal file. Row keys
// alone would not notice a copy that shuffled or truncated tuple bodies.
func readRowBodies(t *testing.T, path string) [][]byte {
	t.Helper()

	r, err := reader.Open(path)
	require.NoError(t, err)

	defer func() { _ = r.Close() }()

	var out [][]byte

	for row, err := range r.Rows() {
		require.NoError(t, err)
		out = append(out, slices.Clone(row.BodyRaw))
	}

	return out
}

func readInstanceUUID(t *testing.T, path string) string {
	t.Helper()

	m, err := reader.ReadHeader(path)
	require.NoError(t, err)

	return m.InstanceUUID.String()
}

// dirEntries returns the names of the files in dir.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}
