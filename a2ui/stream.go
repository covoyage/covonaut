package a2ui

import (
	"encoding/json"
	"io"
)

// Encoder writes A2UI envelopes to an underlying writer as a JSON Lines (JSONL)
// stream: one compact JSON object per line, the protocol's recommended framing.
type Encoder struct {
	enc *json.Encoder
}

// NewEncoder returns an Encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{enc: json.NewEncoder(w)}
}

// Encode writes a single envelope followed by a newline. If the envelope has no
// version set, the current protocol Version is applied.
func (e *Encoder) Encode(env Envelope) error {
	if env.Version == "" {
		env.Version = Version
	}
	return e.enc.Encode(env)
}

// EncodeAll writes a sequence of envelopes in order.
func (e *Encoder) EncodeAll(envs []Envelope) error {
	for _, env := range envs {
		if err := e.Encode(env); err != nil {
			return err
		}
	}
	return nil
}

// Decoder reads a JSONL stream of A2UI envelopes from an underlying reader.
type Decoder struct {
	dec *json.Decoder
}

// NewDecoder returns a Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: json.NewDecoder(r)}
}

// Decode reads the next envelope from the stream. It returns io.EOF when the
// stream is exhausted.
func (d *Decoder) Decode() (Envelope, error) {
	var e Envelope
	if err := d.dec.Decode(&e); err != nil {
		return Envelope{}, err
	}
	if e.Version == "" {
		e.Version = Version
	}
	return e, nil
}

// More reports whether another value is available in the stream.
func (d *Decoder) More() bool { return d.dec.More() }
