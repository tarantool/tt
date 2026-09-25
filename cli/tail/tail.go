package tail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/nxadm/tail"
	"github.com/tarantool/tt/sdk/log"
)

var (
	errNegativeLinesCountIsNotSupported = errors.New("negative lines count is not supported")
)

const (
	blockSize               = 8192
	formatterBufferCapacity = 512
	outputChannelCapacity   = 8
)

// Reader is an interface for reading the last `lines` lines from a file.
type Reader interface {
	// Read reads the last `lines` lines from the file and returns a channel with raw strings.
	Read(ctx context.Context, lines int) (<-chan string, error)
}

type tailer struct {
	name string
}

// NewTailReader creates a new Tailer that reads the last lines from the file with [tail] library.
func NewTailReader(fileName string) Reader {
	return &tailer{name: fileName}
}

// Read implements the Tailer interface.
func (t *tailer) Read(ctx context.Context, lines int) (<-chan string, error) {
	fmt := func(s string) string {
		return s
	}

	return TailN(ctx, fmt, t.name, lines)
}

// LogFormatter is a function used to format log string before output.
type LogFormatter func(str string) string

// NewLogFormatter creates a function to make log prefix colored.
func NewLogFormatter(prefix string, color color.Color) LogFormatter {
	buf := strings.Builder{}
	buf.Grow(formatterBufferCapacity)

	return func(str string) string {
		buf.Reset()

		_, _ = color.Fprint(&buf, prefix)
		buf.WriteString(str)

		return buf.String()
	}
}

// newTailReader returns a reader for last count lines.
func newTailReader(ctx context.Context, reader io.ReadSeeker, count int) (io.Reader, int64, error) {
	end, err := reader.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to seek to the end: %w", err)
	}

	if count <= 0 {
		return &io.LimitedReader{R: reader, N: 0}, end, nil
	}

	startPos := end
	// Skip last char because it can be new-line. For example, tail reader for 'line\n' and n==1
	// should not count last \n as a line.
	readOffset := end - 1

	buf := make([]byte, blockSize)
	linesFound := 0

	for readOffset != 0 && linesFound != count {
		select {
		case <-ctx.Done():
			return nil, 0, fmt.Errorf("stopped looking for the last lines: %w", ctx.Err())
		default:
		}

		limitedReader := io.LimitedReader{R: reader, N: int64(len(buf))}

		readOffset -= limitedReader.N

		if readOffset < 0 {
			limitedReader.N += readOffset

			readOffset = 0
		}

		readOffset, err = reader.Seek(readOffset, io.SeekStart)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to seek back: %w", err)
		}

		readBytes, err := limitedReader.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, startPos, fmt.Errorf("failed to read: %w", err)
		}

		for index := readBytes - 1; index > 0; index-- {
			if buf[index] == '\n' {
				// In case of \n\n\n bytes, start position should not be moved one byte forward.
				if startPos-(readOffset+int64(index)) == 1 {
					startPos = readOffset + int64(index)
				} else {
					startPos = readOffset + int64(index) + 1
				}

				linesFound++
				if linesFound == count {
					break
				}
			}
		}
	}

	if linesFound == count {
		_, _ = reader.Seek(startPos, io.SeekStart)
		return &io.LimitedReader{R: reader, N: end - startPos}, startPos, nil
	}

	_, _ = reader.Seek(0, io.SeekStart)

	return &io.LimitedReader{R: reader, N: end}, 0, nil
}

// TailN calls sends last count lines of the file to the channel.
func TailN(ctx context.Context, logFormatter LogFormatter, fileName string,
	count int,
) (<-chan string, error) {
	if count < 0 {
		return nil, errNegativeLinesCountIsNotSupported
	}

	file, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf("cannot open %q: %w", fileName, err)
	}

	reader, _, err := newTailReader(ctx, file, count)
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	scanner := bufio.NewScanner(reader)
	out := make(chan string, outputChannelCapacity)

	go func() {
		defer close(out)
		defer func() {
			_ = file.Close()
		}()

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case out <- logFormatter(scanner.Text()):
			}
		}
	}()

	return out, nil
}

// Follow sends to the channel each new line from the file as it grows.
func Follow(ctx context.Context, out chan<- string, logFormatter LogFormatter, fileName string,
	count int, group *sync.WaitGroup,
) error {
	file, err := os.Open(fileName)
	if err != nil {
		return fmt.Errorf("cannot open %q: %w", fileName, err)
	}

	defer func() {
		_ = file.Close()
	}()

	_, startPos, err := newTailReader(ctx, file, count)
	if err != nil {
		return err
	}

	fileTail, err := tail.TailFile(fileName, tail.Config{
		Location: &tail.SeekInfo{
			Offset: startPos,
			Whence: io.SeekStart,
		},
		MustExist:     true,
		Follow:        true,
		ReOpen:        true,
		CompleteLines: false,
		Logger:        tail.DiscardingLogger,
	})
	if err != nil {
		return fmt.Errorf("cannot follow %q: %w", fileName, err)
	}

	group.Go(func() {
		for {
			select {
			case <-ctx.Done():
				_ = fileTail.Stop()
				_ = fileTail.Wait()

				return
			case line, more := <-fileTail.Lines:
				if !more {
					err := fileTail.Stop()
					if err != nil {
						log.Error(err.Error())
					} else {
						log.Errorf("The log file %q is unavailable for reading. Exiting.")
					}

					return
				}

				out <- logFormatter(line.Text)
			}
		}
	})

	return nil
}
