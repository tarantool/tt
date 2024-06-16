package tail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/nxadm/tail"
)

const blockSize = 8192

// LogFormatter is a function used to format log string before output.
type LogFormatter func(str string) string

// NewLogFormatter creates a function to make log prefix colored.
func NewLogFormatter(prefix string, color color.Color) LogFormatter {
	buf := strings.Builder{}
	buf.Grow(512)
	return func(str string) string {
		buf.Reset()
		color.Fprint(&buf, prefix)
		buf.WriteString(str)
		return buf.String()
	}
}

// newTailReader returns a reader for last count lines.
func newTailReader(ctx context.Context, reader io.ReadSeeker, count int) (io.Reader, int64, error) {
	end, err := reader.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, 0, err
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
			return nil, 0, ctx.Err()
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
			return nil, 0, err
		}
		readBytes, err := limitedReader.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, startPos, fmt.Errorf("failed to read: %s", err)
		}
		for i := readBytes - 1; i > 0; i-- {
			if buf[i] == '\n' {
				// In case of \n\n\n bytes, start position should not be moved one byte forward.
				if startPos-(readOffset+int64(i)) == 1 {
					startPos = readOffset + int64(i)
				} else {
					startPos = readOffset + int64(i) + 1
				}

				linesFound++
				if linesFound == count {
					break
				}
			}
		}
	}
	if linesFound == count {
		reader.Seek(startPos, io.SeekStart)
		return &io.LimitedReader{R: reader, N: end - startPos}, startPos, nil
	}
	reader.Seek(0, io.SeekStart)
	return &io.LimitedReader{R: reader, N: end}, 0, nil
}

// TailN calls sends last n lines of the file to the channel.
func TailN(ctx context.Context, logFormatter LogFormatter, fileName string,
	n int) (<-chan string, error) {
	if n < 0 {
		return nil, fmt.Errorf("negative lines count is not supported")
	}

	file, err := os.Open(fileName)
	if err != nil {
		return nil, fmt.Errorf("cannot open %q: %w", fileName, err)
	}

	reader, _, err := newTailReader(ctx, file, n)
	if err != nil {
		file.Close()
		return nil, err
	}

	scanner := bufio.NewScanner(reader)
	out := make(chan string, 8)
	go func() {
		defer close(out)
		defer file.Close()
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
	n int) error {
	file, err := os.Open(fileName)
	if err != nil {
		return fmt.Errorf("cannot open %q: %w", fileName, err)
	}
	defer file.Close()

	_, startPos, err := newTailReader(ctx, file, n)
	if err != nil {
		return err
	}

	t, err := tail.TailFile(fileName, tail.Config{
		Location: &tail.SeekInfo{
			Offset: startPos,
			Whence: io.SeekStart,
		},
		MustExist:     true,
		Follow:        true,
		ReOpen:        true,
		CompleteLines: false,
		Logger:        tail.DiscardingLogger})
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				t.Wait()
				return
			case line := <-t.Lines:
				out <- logFormatter(line.Text)
			}
		}
	}()
	return nil
}
