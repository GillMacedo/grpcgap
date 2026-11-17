package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// jnode is one JSON value with the byte offset where it starts, so a
// finding can name the line. Objects keep every member in order,
// duplicates included, because encoding/json reads them all.
type jnode struct {
	kind    byte // 'o' object, 'a' array, 's' string, 'n' number, 'b' bool, 'z' null
	off     int
	raw     []byte // the value's bytes as written, for scalars
	str     string // the decoded string, for strings
	boolean bool
	members []jmember
	elems   []*jnode
}

type jmember struct {
	key string
	off int
	val *jnode
}

func (n *jnode) kindName() string {
	switch n.kind {
	case 'o':
		return "object"
	case 'a':
		return "array"
	case 's':
		return "string"
	case 'n':
		return "number"
	case 'b':
		return "bool"
	}
	return "null"
}

// parseJSON reads exactly one JSON value, the way json.Unmarshal would
// accept it: white space may surround it, nothing else may follow.
func parseJSON(data []byte) (*jnode, error) {
	p := &jparser{data: data, dec: json.NewDecoder(bytes.NewReader(data))}
	p.dec.UseNumber()
	n, err := p.value()
	if err != nil {
		return nil, err
	}
	if _, err := p.dec.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("invalid character after top-level value")
		}
		return nil, err
	}
	return n, nil
}

type jparser struct {
	data []byte
	dec  *json.Decoder
}

// next returns the next token and the offset where it starts.
func (p *jparser) next() (json.Token, int, []byte, error) {
	before := int(p.dec.InputOffset())
	tok, err := p.dec.Token()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, 0, nil, err
	}
	after := int(p.dec.InputOffset())
	start := before
	for start < after {
		c := p.data[start]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' || c == ':' {
			start++
			continue
		}
		break
	}
	return tok, start, p.data[start:after], nil
}

func (p *jparser) value() (*jnode, error) {
	tok, off, raw, err := p.next()
	if err != nil {
		return nil, err
	}
	return p.fromToken(tok, off, raw)
}

func (p *jparser) fromToken(tok json.Token, off int, raw []byte) (*jnode, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n := &jnode{kind: 'o', off: off}
			for {
				kt, koff, _, err := p.next()
				if err != nil {
					return nil, err
				}
				if d, ok := kt.(json.Delim); ok && d == '}' {
					return n, nil
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("object key at offset %d is not a string", koff)
				}
				v, err := p.value()
				if err != nil {
					return nil, err
				}
				n.members = append(n.members, jmember{key: key, off: koff, val: v})
			}
		case '[':
			n := &jnode{kind: 'a', off: off}
			for {
				et, eoff, eraw, err := p.next()
				if err != nil {
					return nil, err
				}
				if d, ok := et.(json.Delim); ok && d == ']' {
					return n, nil
				}
				v, err := p.fromToken(et, eoff, eraw)
				if err != nil {
					return nil, err
				}
				n.elems = append(n.elems, v)
			}
		}
		return nil, fmt.Errorf("unexpected %q at offset %d", rune(t), off)
	case string:
		return &jnode{kind: 's', off: off, raw: raw, str: t}, nil
	case json.Number:
		return &jnode{kind: 'n', off: off, raw: raw}, nil
	case bool:
		return &jnode{kind: 'b', off: off, raw: raw, boolean: t}, nil
	case nil:
		return &jnode{kind: 'z', off: off, raw: raw}, nil
	}
	return nil, fmt.Errorf("unexpected token at offset %d", off)
}
