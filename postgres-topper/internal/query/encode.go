package query

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"postgres-topper/internal/catalog"
)

// EncodeRows writes rows as a JSON array of objects into buf, one object
// per row with keys in projection order, and returns the row count. It
// closes rows. The projection must match the statement the rows came from.
func EncodeRows(buf *bytes.Buffer, rows pgx.Rows, proj []Projected) (int, error) {
	enc := NewArrayEncoder(buf)
	if err := enc.Append(rows, proj); err != nil {
		return enc.Count(), err
	}
	return enc.Close(), nil
}

// ArrayEncoder writes one JSON array of row objects that may come from
// several result sets (an upsert of many rows runs one statement per row).
type ArrayEncoder struct {
	buf *bytes.Buffer
	n   int
}

// NewArrayEncoder opens the array.
func NewArrayEncoder(buf *bytes.Buffer) *ArrayEncoder {
	buf.WriteByte('[')
	return &ArrayEncoder{buf: buf}
}

// Append writes every row of rows as an object and closes rows.
func (a *ArrayEncoder) Append(rows pgx.Rows, proj []Projected) error {
	defer rows.Close()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return err
		}
		if len(vals) != len(proj) {
			return &Error{Status: 500, Msg: "projection mismatch"}
		}
		if a.n > 0 {
			a.buf.WriteByte(',')
		}
		a.buf.WriteByte('{')
		for i, p := range proj {
			if i > 0 {
				a.buf.WriteByte(',')
			}
			a.buf.Write(p.key)
			a.buf.WriteByte(':')
			if err := encodeValue(a.buf, vals[i], p.Kind); err != nil {
				return err
			}
		}
		a.buf.WriteByte('}')
		a.n++
	}
	return rows.Err()
}

// Count returns the rows written so far.
func (a *ArrayEncoder) Count() int { return a.n }

// Close writes the closing bracket and returns the row count.
func (a *ArrayEncoder) Close() int {
	a.buf.WriteByte(']')
	return a.n
}

// encodeValue writes one scanned value as JSON according to kind.
func encodeValue(buf *bytes.Buffer, v any, kind catalog.ValueKind) error {
	if v == nil {
		buf.WriteString("null")
		return nil
	}
	switch kind {
	case catalog.ValueRawJSON:
		if s, ok := v.(string); ok {
			buf.WriteString(s)
			return nil
		}
	case catalog.ValueText:
		if s, ok := v.(string); ok {
			return writeJSONString(buf, s)
		}
	}
	switch x := v.(type) {
	case bool:
		buf.WriteString(strconv.FormatBool(x))
	case int16:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
	case int32:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
	case int:
		buf.WriteString(strconv.Itoa(x))
	case float32:
		return writeFloat(buf, float64(x), 32)
	case float64:
		return writeFloat(buf, x, 64)
	case string:
		return writeJSONString(buf, x)
	case []byte:
		return writeJSON(buf, x) // base64, as encoding/json does
	case time.Time:
		return writeJSONString(buf, x.UTC().Format(time.RFC3339Nano))
	case pgtype.InfinityModifier:
		return writeJSONString(buf, x.String())
	default:
		return writeJSON(buf, v)
	}
	return nil
}

// writeFloat renders a float, spelling NaN and the infinities as strings
// since JSON has no representation for them.
func writeFloat(buf *bytes.Buffer, f float64, bits int) error {
	switch {
	case math.IsNaN(f):
		return writeJSONString(buf, "NaN")
	case math.IsInf(f, 1):
		return writeJSONString(buf, "Infinity")
	case math.IsInf(f, -1):
		return writeJSONString(buf, "-Infinity")
	}
	buf.WriteString(strconv.FormatFloat(f, 'g', -1, bits))
	return nil
}

func writeJSONString(buf *bytes.Buffer, s string) error {
	return writeJSON(buf, s)
}

func writeJSON(buf *bytes.Buffer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf.Write(b)
	return nil
}
