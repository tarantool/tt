package test_helpers

import "os"

type EntryMock struct {
	os.DirEntry
	EntryName  string
	EntryIsDir bool
	EntryType  os.FileMode
	EntryInfo  os.FileInfo
}

func (mock EntryMock) Name() string {
	return mock.EntryName
}

func (mock EntryMock) IsDir() bool {
	return mock.EntryIsDir
}

func (mock EntryMock) Type() os.FileMode {
	return mock.EntryType
}

func (mock EntryMock) Info() (os.FileInfo, error) {
	return mock.EntryInfo, nil
}
