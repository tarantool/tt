package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	// appHashesFileName is the hashes file of an application.
	appHashesFileName = "hashes.json"
	// envHashesFileName is the hashes file of an environment.
	envHashesFileName = "env_hashes.json"
	// signatureSuffix names the signature of a hashes file after it.
	signatureSuffix = ".sig"
	// sha256Algorithm is the name of the only hash algorithm the provider
	// computes and checks.
	sha256Algorithm = "sha256"

	filesMember = "files"
	pathMember  = "path"
)

var (
	errMalformedHashes = errors.New("malformed hashes file")
	errMissingMember   = errors.New("missing member")
	errNotObject       = errors.New("not an object")
	errNotString       = errors.New("not a string")
	errNull            = errors.New("null")
	errHashCount       = errors.New("exactly one hash besides the path is expected")
	errDigestLength    = errors.New("digest has a wrong length")
	errInvalidPathName = errors.New("path is not valid UTF-8")
)

// fileDigest is a file to list in a hashes file: its path relative to the
// directory of the hashes file, with slashes, and its SHA-256 digest.
type fileDigest struct {
	path   string
	digest []byte
}

// hashRecord is an entry read from a hashes file.
type hashRecord struct {
	// path is the path of the file as the hashes file names it.
	path string
	// algorithm is the name of the hash algorithm of the entry.
	algorithm string
	// digest is the digest the entry holds.
	digest []byte
}

// hashesDocument is the JSON document of a hashes file as it is written.
type hashesDocument struct {
	Files []hashesDocumentEntry `json:"files"`
}

// hashesDocumentEntry is an entry of a hashes file as it is written: the
// path comes first.
type hashesDocumentEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// encodeHashes returns the content of a hashes file that lists digests in
// their order: compact JSON with lowercase hex digests and an empty array,
// never null, when there is nothing to list.
func encodeHashes(digests []fileDigest) ([]byte, error) {
	document := hashesDocument{Files: make([]hashesDocumentEntry, 0, len(digests))}

	for _, file := range digests {
		// JSON strings carry text only: a name that is not UTF-8 would be
		// written as a different name.
		if !utf8.ValidString(file.path) {
			return nil, fmt.Errorf("%w: %q", errInvalidPathName, file.path)
		}

		document.Files = append(document.Files, hashesDocumentEntry{
			Path:   file.path,
			SHA256: hex.EncodeToString(file.digest),
		})
	}

	data, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("failed to encode hashes: %w", err)
	}

	return data, nil
}

// decodeHashes parses data, the content of the hashes file at path. Every
// entry must be an object with a string path and exactly one other member,
// a hash named after its algorithm whose value is hex text; a SHA-256
// digest must be 32 bytes long. Other algorithms are accepted here and
// refused when the files are checked.
func decodeHashes(path string, data []byte) ([]hashRecord, error) {
	var document map[string]json.RawMessage

	err := json.Unmarshal(data, &document)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %w", errMalformedHashes, path, err)
	}

	// JSON null decodes into a nil map.
	if document == nil {
		return nil, fmt.Errorf("%w %q: %w", errMalformedHashes, path, errNotObject)
	}

	rawFiles, ok := document[filesMember]
	if !ok {
		return nil, fmt.Errorf("%w %q: %w %q", errMalformedHashes, path, errMissingMember,
			filesMember)
	}

	var items []json.RawMessage

	err = json.Unmarshal(rawFiles, &items)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %q: %w", errMalformedHashes, path, filesMember, err)
	}

	// JSON null decodes into a nil slice, an empty array into an empty one.
	if items == nil {
		return nil, fmt.Errorf("%w %q: %q: %w", errMalformedHashes, path, filesMember, errNull)
	}

	records := make([]hashRecord, 0, len(items))

	for index, item := range items {
		record, err := decodeHashRecord(item)
		if err != nil {
			return nil, fmt.Errorf("%w %q: entry %d: %w", errMalformedHashes, path, index, err)
		}

		records = append(records, record)
	}

	return records, nil
}

// decodeHashRecord parses an entry of a hashes file.
func decodeHashRecord(item json.RawMessage) (hashRecord, error) {
	var members map[string]json.RawMessage

	err := json.Unmarshal(item, &members)
	if err != nil {
		return hashRecord{}, fmt.Errorf("%w: %w", errNotObject, err)
	}

	if members == nil {
		return hashRecord{}, errNotObject
	}

	rawPath, ok := members[pathMember]
	if !ok {
		return hashRecord{}, fmt.Errorf("%w %q", errMissingMember, pathMember)
	}

	path, err := decodeString(rawPath)
	if err != nil {
		return hashRecord{}, fmt.Errorf("%q: %w", pathMember, err)
	}

	delete(members, pathMember)

	if len(members) != 1 {
		return hashRecord{}, fmt.Errorf("%w, found %d", errHashCount, len(members))
	}

	var (
		algorithm string
		rawDigest json.RawMessage
	)

	// The only member left is the hash.
	for name, value := range members {
		algorithm, rawDigest = name, value
	}

	digest, err := decodeDigest(algorithm, rawDigest)
	if err != nil {
		return hashRecord{}, fmt.Errorf("hash %q of %q: %w", algorithm, path, err)
	}

	return hashRecord{path: path, algorithm: algorithm, digest: digest}, nil
}

// decodeDigest parses the hex text of a digest. A SHA-256 digest must be
// 32 bytes long; the digests of other algorithms are not checked here.
func decodeDigest(algorithm string, raw json.RawMessage) ([]byte, error) {
	text, err := decodeString(raw)
	if err != nil {
		return nil, err
	}

	digest, err := hex.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("not hex: %w", err)
	}

	if algorithm == sha256Algorithm && len(digest) != sha256.Size {
		return nil, fmt.Errorf("%w: %d bytes, %d expected", errDigestLength, len(digest),
			sha256.Size)
	}

	return digest, nil
}

// decodeString parses a JSON value that must be a string.
func decodeString(raw json.RawMessage) (string, error) {
	var text *string

	err := json.Unmarshal(raw, &text)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNotString, err)
	}

	// JSON null decodes into a nil pointer.
	if text == nil {
		return "", fmt.Errorf("%w: %w", errNotString, errNull)
	}

	return *text, nil
}
