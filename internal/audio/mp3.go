package audio

import (
	"io"
	"sync"

	mp3 "github.com/hajimehoshi/go-mp3"
)

// MP3StreamDecoder incrementally decodes an MP3 byte stream into s16le PCM.
type MP3StreamDecoder struct {
	pr *io.PipeReader
	pw *io.PipeWriter

	done chan struct{}

	mu         sync.Mutex
	pcm        []byte
	sampleRate int
	err        error
}

// NewMP3StreamDecoder creates and starts a streaming MP3 decoder.
func NewMP3StreamDecoder() *MP3StreamDecoder {
	pr, pw := io.Pipe()
	decoder := &MP3StreamDecoder{
		pr:   pr,
		pw:   pw,
		done: make(chan struct{}),
	}
	go decoder.run()
	return decoder
}

// Push appends encoded MP3 bytes and returns decoded PCM produced so far.
func (d *MP3StreamDecoder) Push(data []byte) ([]PCMData, error) {
	if len(data) > 0 {
		if _, err := d.pw.Write(data); err != nil {
			if decodeErr := d.decodeErr(); decodeErr != nil {
				return nil, decodeErr
			}
			return nil, err
		}
	}

	return d.drainPCM(), d.decodeErr()
}

// Finish closes the input stream and returns all remaining decoded PCM.
func (d *MP3StreamDecoder) Finish() ([]PCMData, error) {
	_ = d.pw.Close()
	<-d.done
	return d.drainPCM(), d.decodeErr()
}

func (d *MP3StreamDecoder) run() {
	defer close(d.done)
	defer func() {
		_ = d.pr.Close()
	}()

	decoder, err := mp3.NewDecoder(d.pr)
	if err != nil {
		if err != io.EOF && err != io.ErrClosedPipe {
			d.setErr(err)
		}
		return
	}

	d.mu.Lock()
	d.sampleRate = decoder.SampleRate()
	d.mu.Unlock()

	buffer := make([]byte, 4096)
	for {
		n, err := decoder.Read(buffer)
		if n > 0 {
			d.appendPCM(buffer[:n])
		}
		if err != nil {
			if err != io.EOF && err != io.ErrClosedPipe {
				d.setErr(err)
			}
			return
		}
	}
}

func (d *MP3StreamDecoder) appendPCM(data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pcm = append(d.pcm, data...)
}

func (d *MP3StreamDecoder) drainPCM() []PCMData {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.pcm) == 0 {
		return nil
	}
	data := make([]byte, len(d.pcm))
	copy(data, d.pcm)
	d.pcm = d.pcm[:0]
	return []PCMData{{
		SampleRate: d.sampleRate,
		Channels:   2,
		Format:     PCMFormatS16LE,
		Data:       data,
	}}
}

func (d *MP3StreamDecoder) setErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.err == nil {
		d.err = err
	}
}

func (d *MP3StreamDecoder) decodeErr() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}
