package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Stream writes a result to stdout one item at a time, each as soon as it
// is emitted. It is opened by [Printer.Stream] and must be closed; until
// then the Printer refuses any other output. A Stream is safe for
// concurrent use: items emitted from several goroutines are written one
// after another, never interleaved.
type Stream struct {
	printer *Printer
	// encoder is nil in the human format, where items render themselves.
	encoder StreamEncoder

	// mu serialises items and guards buf and closed. buf is empty between
	// calls: flush and every failure path reset it.
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

// flusher is a stdout that holds writes back until it is flushed.
type flusher interface {
	Flush() error
}

// Stream opens a stream on stdout. In the human format every item is
// rendered by its Human method; in a machine format it is encoded by the
// format's stream encoder, and a format without one is ErrNoStreamForm. A
// Printer has at most one open stream: while it is open, Stream, Print,
// Printf and Emit return ErrStreamOpen.
func (p *Printer) Stream() (*Stream, error) {
	var newEncoder NewStreamEncoder

	if p.format != FormatHuman {
		newEncoder = p.streamEncoders[p.format]
		if newEncoder == nil {
			return nil, fmt.Errorf("%w: %q", ErrNoStreamForm, p.format)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.streaming {
		return nil, ErrStreamOpen
	}

	stream := &Stream{
		printer: p,
		encoder: nil,
		mu:      sync.Mutex{},
		buf:     bytes.Buffer{},
		closed:  false,
	}

	if newEncoder != nil {
		stream.encoder = newEncoder(&stream.buf)

		err := stream.flush()
		if err != nil {
			return nil, err
		}
	}

	p.streaming = true

	return stream, nil
}

// Emit writes item to stdout: its Human rendering in the human format, its
// encoding otherwise. The item is rendered in full before anything is
// written, so an item that fails to render writes nothing, and the stream
// stays open for the next one. Items emitted before stay on stdout
// whatever happens after them. When stdout can be flushed it is flushed
// after every item.
func (s *Stream) Emit(item Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStreamClosed
	}

	if s.encoder == nil {
		err := item.Human(&s.buf)
		if err != nil {
			s.buf.Reset()

			return fmt.Errorf("rendering output: %w", err)
		}
	} else {
		err := s.encoder.Encode(item)
		if err != nil {
			s.buf.Reset()

			return fmt.Errorf("encoding output as %s: %w", s.printer.format, err)
		}
	}

	return s.flush()
}

// Close ends the stream: the stream encoder writes what follows the last
// item, and the Printer accepts other output again, even when Close fails.
// Closing a closed stream does nothing.
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	defer func() {
		s.printer.mu.Lock()

		s.printer.streaming = false
		s.printer.mu.Unlock()
	}()

	if s.encoder == nil {
		return nil
	}

	err := s.encoder.Close()
	if err != nil {
		s.buf.Reset()

		return fmt.Errorf("encoding output as %s: %w", s.printer.format, err)
	}

	return s.flush()
}

// flush writes what buf holds to stdout in one write and flushes stdout if
// it can be flushed. An empty buf writes and flushes nothing.
func (s *Stream) flush() error {
	defer s.buf.Reset()

	if s.buf.Len() == 0 {
		return nil
	}

	_, err := s.printer.streams.Out.Write(s.buf.Bytes())
	if err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	if out, ok := s.printer.streams.Out.(flusher); ok {
		err = out.Flush()
		if err != nil {
			return fmt.Errorf("flushing output: %w", err)
		}
	}

	return nil
}

// jsonLines streams JSON Lines: every item normalised and encoded as one
// compact JSON value followed by a newline.
type jsonLines struct {
	encoder *json.Encoder
}

func newJSONLines(w io.Writer) StreamEncoder {
	return jsonLines{encoder: json.NewEncoder(w)}
}

// Encode writes item as one line of JSON.
func (j jsonLines) Encode(item any) error {
	normalized, err := Normalize(item)
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	err = j.encoder.Encode(normalized)
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}

	return nil
}

// Close writes nothing: JSON Lines has no end marker.
func (jsonLines) Close() error {
	return nil
}
