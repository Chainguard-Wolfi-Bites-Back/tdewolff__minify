// Package svg minifies SVG1.1 following the specifications at http://www.w3.org/TR/SVG11/.
package svg // import "github.com/tdewolff/minify/svg"

import (
	"io"

	"github.com/tdewolff/buffer"
	"github.com/tdewolff/minify"
	minifyCSS "github.com/tdewolff/minify/css"
	"github.com/tdewolff/parse"
	"github.com/tdewolff/parse/css"
	"github.com/tdewolff/parse/svg"
	"github.com/tdewolff/parse/xml"
)

var (
	voidBytes       = []byte("/>")
	isBytes         = []byte("=")
	spaceBytes      = []byte(" ")
	cdataStartBytes = []byte("<![CDATA[")
	cdataEndBytes   = []byte("]]>")
	pathBytes       = []byte("<path")
	dBytes          = []byte("d")
	zeroBytes       = []byte("0")
)

////////////////////////////////////////////////////////////////

// Minifier is an SVG minifier.
type Minifier struct{}

// Minify minifies SVG data, it reads from r and writes to w.
func Minify(m *minify.M, w io.Writer, r io.Reader, params map[string]string) error {
	return (&Minifier{}).Minify(m, w, r, params)
}

// Minify minifies SVG data, it reads from r and writes to w.
func (o *Minifier) Minify(m *minify.M, w io.Writer, r io.Reader, _ map[string]string) error {
	var tag svg.Hash
	defaultStyleType := "text/css"
	defaultInlineStyleType := "text/css;inline=1"

	minifyBuffer := buffer.NewWriter(make([]byte, 0, 64))
	attrByteBuffer := make([]byte, 0, 64)
	pathDataBuffer := PathData{}
	gStack := make([]bool, 0)

	l := xml.NewLexer(r)
	tb := NewTokenBuffer(l)
	for {
		t := *tb.Shift()
	SWITCH:
		switch t.TokenType {
		case xml.ErrorToken:
			if l.Err() == io.EOF {
				return nil
			}
			return l.Err()
		case xml.TextToken:
			t.Data = parse.ReplaceMultipleWhitespace(parse.TrimWhitespace(t.Data))
			if tag == svg.Style && len(t.Data) > 0 {
				if err := m.Minify(defaultStyleType, w, buffer.NewReader(t.Data)); err != nil {
					if err == minify.ErrNotExist { // no minifier, write the original
						if _, err := w.Write(t.Data); err != nil {
							return err
						}
					} else {
						return err
					}
				}
			} else if _, err := w.Write(t.Data); err != nil {
				return err
			}
		case xml.CDATAToken:
			if tag == svg.Style {
				minifyBuffer.Reset()
				if err := m.Minify(defaultStyleType, minifyBuffer, buffer.NewReader(t.Text)); err != nil && err != minify.ErrNotExist {
					return err
				} else if err != minify.ErrNotExist {
					t.Data = append(t.Data[:9], minifyBuffer.Bytes()...)
					t.Text = t.Data[9:]
					t.Data = append(t.Data, cdataEndBytes...)
				}
			}
			var useText bool
			if t.Text, useText = xml.EscapeCDATAVal(&attrByteBuffer, t.Text); useText {
				t.Text = parse.ReplaceMultipleWhitespace(parse.TrimWhitespace(t.Text))
				if _, err := w.Write(t.Text); err != nil {
					return err
				}
			} else if _, err := w.Write(t.Data); err != nil {
				return err
			}
		case xml.StartTagPIToken:
			for {
				if t := *tb.Shift(); t.TokenType == xml.StartTagClosePIToken || t.TokenType == xml.ErrorToken {
					break
				}
			}
		case xml.StartTagToken:
			tag = t.Hash
			if containerTagMap[tag] { // skip empty containers
				i := 0
				for {
					next := tb.Peek(i)
					i++
					if next.TokenType == xml.EndTagToken && next.Hash == tag || next.TokenType == xml.StartTagCloseVoidToken || next.TokenType == xml.ErrorToken {
						for j := 0; j < i; j++ {
							tb.Shift()
						}
						break SWITCH
					} else if next.TokenType != xml.AttributeToken && next.TokenType != xml.StartTagCloseToken {
						break
					}
				}
				if tag == svg.G {
					if tb.Peek(0).TokenType == xml.StartTagCloseToken {
						gStack = append(gStack, false)
						tb.Shift()
						break
					}
					gStack = append(gStack, true)
				}
			} else if tag == svg.Metadata {
				skipTag(tb, tag)
				break
			} else if tag == svg.Line {
				shortenLine(tb, &t, &pathDataBuffer)
			} else if tag == svg.Rect && !shortenRect(tb, &t, &pathDataBuffer) {
				skipTag(tb, tag)
				break
			} else if tag == svg.Polygon || tag == svg.Polyline {
				shortenPoly(tb, &t, &pathDataBuffer)
			}
			if _, err := w.Write(t.Data); err != nil {
				return err
			}
		case xml.AttributeToken:
			if len(t.AttrVal) == 0 || t.Text == nil { // data is nil when attribute has been removed
				continue
			}
			attr := t.Hash
			val := t.AttrVal
			if n, m := parse.Dimension(val); n+m == len(val) { // TODO: inefficient, temporary measure
				val, _ = o.shortenDimension(val)
			}
			if attr == svg.Xml_Space && parse.Equal(val, []byte("preserve")) ||
				tag == svg.Svg && (attr == svg.Version && parse.Equal(val, []byte("1.1")) ||
					attr == svg.X && parse.Equal(val, []byte("0")) ||
					attr == svg.Y && parse.Equal(val, []byte("0")) ||
					attr == svg.Width && parse.Equal(val, []byte("100%")) ||
					attr == svg.Height && parse.Equal(val, []byte("100%")) ||
					attr == svg.PreserveAspectRatio && parse.Equal(val, []byte("xMidYMid meet")) ||
					attr == svg.BaseProfile && parse.Equal(val, []byte("none")) ||
					attr == svg.ContentScriptType && parse.Equal(val, []byte("application/ecmascript")) ||
					attr == svg.ContentStyleType && parse.Equal(val, []byte("text/css"))) ||
				tag == svg.Style && attr == svg.Type && parse.Equal(val, []byte("text/css")) {
				continue
			}

			if _, err := w.Write(spaceBytes); err != nil {
				return err
			}
			if _, err := w.Write(t.Text); err != nil {
				return err
			}
			if _, err := w.Write(isBytes); err != nil {
				return err
			}

			if tag == svg.Svg && attr == svg.ContentStyleType {
				val = minify.ContentType(val)
				defaultStyleType = string(val)
				defaultInlineStyleType = defaultStyleType + ";inline=1"
			} else if attr == svg.Style {
				minifyBuffer.Reset()
				if m.Minify(defaultInlineStyleType, minifyBuffer, buffer.NewReader(val)) == nil {
					val = minifyBuffer.Bytes()
				}
			} else if attr == svg.D {
				val = ShortenPathData(val, &pathDataBuffer)
			} else if attr == svg.ViewBox {
				j := 0
				newVal := val[:0]
				for i := 0; i < 4; i++ {
					if i != 0 {
						if j >= len(val) || val[j] != ' ' && val[j] != ',' {
							newVal = append(newVal, val[j:]...)
							break
						}
						newVal = append(newVal, ' ')
						j++
					}
					if dim, n := shortenDimension(val[j:]); n > 0 {
						newVal = append(newVal, dim...)
						j += n
					} else {
						newVal = append(newVal, val[j:]...)
						break
					}
				}
				val = newVal
			} else if colorAttrMap[attr] && len(val) > 0 {
				parse.ToLower(val)
				if val[0] == '#' {
					if name, ok := minifyCSS.ShortenColorHex[string(val)]; ok {
						val = name
					} else if len(val) == 7 && val[1] == val[2] && val[3] == val[4] && val[5] == val[6] {
						val[2] = val[3]
						val[3] = val[5]
						val = val[:4]
					}
				} else if hex, ok := minifyCSS.ShortenColorName[css.ToHash(val)]; ok {
					val = hex
				} else if len(val) > 5 && parse.Equal(val[:4], []byte("rgb(")) && val[len(val)-1] == ')' {
					// TODO: handle rgb(x, y, z) and hsl(x, y, z)
				}
			} else if n, m := parse.Dimension(val); attr != svg.Points && n+m == len(val) { // TODO: inefficient, temporary measure
				val, _ = shortenDimension(val)
			}

			// prefer single or double quotes depending on what occurs more often in value
			val = xml.EscapeAttrVal(&attrByteBuffer, val)
			if _, err := w.Write(val); err != nil {
				return err
			}
		case xml.StartTagCloseToken:
			next := tb.Peek(0)
			skipExtra := false
			if next.TokenType == xml.TextToken && parse.IsAllWhitespace(next.Data) {
				next = tb.Peek(1)
				skipExtra = true
			}
			if next.TokenType == xml.EndTagToken {
				// collapse empty tags to single void tag
				tb.Shift()
				if skipExtra {
					tb.Shift()
				}
				if _, err := w.Write(voidBytes); err != nil {
					return err
				}
			} else {
				if _, err := w.Write(t.Data); err != nil {
					return err
				}
			}
		case xml.StartTagCloseVoidToken:
			tag = 0
			if _, err := w.Write(t.Data); err != nil {
				return err
			}
		case xml.EndTagToken:
			tag = 0
			if t.Hash == svg.G && len(gStack) > 0 {
				if !gStack[len(gStack)-1] {
					break
				}
				gStack = gStack[:len(gStack)-1]
			}
			if len(t.Data) > 2+len(t.Text) {
				t.Data[2+len(t.Text)] = '>'
				if _, err := w.Write(t.Data[:2+len(t.Text)+1]); err != nil {
					return err
				}
			} else if _, err := w.Write(t.Data); err != nil {
				return err
			}
		}
	}
}

func shortenDimension(b []byte) ([]byte, int) {
	if n, m := parse.Dimension(b); n > 0 {
		unit := b[n : n+m]
		b = minify.Number(b[:n])
		if len(b) != 1 || b[0] != '0' {
			if m == 2 && unit[0] == 'p' && unit[1] == 'x' {
				unit = nil
			} else if m > 1 { // only percentage is length 1
				parse.ToLower(unit)
			}
			b = append(b, unit...)
		}
		return b, n + m
	}
	return b, 0
}

func shortenLine(tb *TokenBuffer, t *Token, pathDataBuffer *PathData) {
	x1, y1, x2, y2 := zeroBytes, zeroBytes, zeroBytes, zeroBytes
	attrs, replacee := tb.Attributes(svg.X1, svg.Y1, svg.X2, svg.Y2)
	if attrs[0] != nil {
		x1 = minify.Number(attrs[0].AttrVal)
		attrs[0].Text = nil
	}
	if attrs[1] != nil {
		y1 = minify.Number(attrs[1].AttrVal)
		attrs[1].Text = nil
	}
	if attrs[2] != nil {
		x2 = minify.Number(attrs[2].AttrVal)
		attrs[2].Text = nil
	}
	if attrs[3] != nil {
		y2 = minify.Number(attrs[3].AttrVal)
		attrs[3].Text = nil
	}

	d := make([]byte, 0, 5+len(x1)+len(y1)+len(x2)+len(y2))
	d = append(d, 'M')
	d = append(d, x1...)
	d = append(d, ' ')
	d = append(d, y1...)
	d = append(d, 'L')
	d = append(d, x2...)
	d = append(d, ' ')
	d = append(d, y2...)
	d = append(d, 'z')
	d = ShortenPathData(d, pathDataBuffer)

	t.Data = pathBytes
	replacee.Text = dBytes
	replacee.AttrVal = d
}

func shortenRect(tb *TokenBuffer, t *Token, pathDataBuffer *PathData) bool {
	attrs, replacee := tb.Attributes(svg.X, svg.Y, svg.Width, svg.Height, svg.Rx, svg.Ry)
	if attrs[4] == nil && attrs[5] == nil {
		x, y, w, h := zeroBytes, zeroBytes, zeroBytes, zeroBytes
		if attrs[0] != nil {
			x = minify.Number(attrs[0].AttrVal)
			attrs[0].Text = nil
		}
		if attrs[1] != nil {
			y = minify.Number(attrs[1].AttrVal)
			attrs[1].Text = nil
		}
		if attrs[2] != nil {
			w = minify.Number(attrs[2].AttrVal)
			attrs[2].Text = nil
		}
		if attrs[3] != nil {
			h = minify.Number(attrs[3].AttrVal)
			attrs[3].Text = nil
		}
		if len(w) == 0 || len(w) == 1 && w[0] == '0' || len(h) == 0 || len(h) == 1 && h[0] == '0' {
			return false
		}

		d := make([]byte, 0, 7+2*len(x)+2*len(y)+len(w)+len(h))
		d = append(d, 'M')
		d = append(d, x...)
		d = append(d, ' ')
		d = append(d, y...)
		d = append(d, 'h')
		d = append(d, w...)
		d = append(d, 'v')
		d = append(d, h...)
		d = append(d, 'H')
		d = append(d, x...)
		d = append(d, 'z')
		d = ShortenPathData(d, pathDataBuffer)

		t.Data = pathBytes
		replacee.Text = dBytes
		replacee.AttrVal = d
	}
	return true
}

func shortenPoly(tb *TokenBuffer, t *Token, pathDataBuffer *PathData) {
	attrs, replacee := tb.Attributes(svg.Points)
	if attrs[0] != nil {
		points := attrs[0].AttrVal

		i := 0
		for i < len(points) && (points[i] == ' ' || points[i] == ',' || points[i] == '\n' || points[i] == '\r' || points[i] == '\t') {
			i++
		}
		if i == len(points) {
			return
		}
		for i < len(points) && !(points[i] == ' ' || points[i] == ',' || points[i] == '\n' || points[i] == '\r' || points[i] == '\t') {
			i++
		}
		for i < len(points) && (points[i] == ' ' || points[i] == ',' || points[i] == '\n' || points[i] == '\r' || points[i] == '\t') {
			i++
		}
		if i == len(points) {
			return
		}
		for i < len(points) && !(points[i] == ' ' || points[i] == ',' || points[i] == '\n' || points[i] == '\r' || points[i] == '\t') {
			i++
		}
		endMoveTo := i
		for i < len(points) && (points[i] == ' ' || points[i] == ',' || points[i] == '\n' || points[i] == '\r' || points[i] == '\t') {
			i++
		}
		startLineTo := i

		d := make([]byte, 0, len(points))
		d = append(d, 'M')
		d = append(d, points[:endMoveTo]...)
		d = append(d, 'L')
		d = append(d, points[startLineTo:]...)
		if t.Hash == svg.Polygon {
			d = append(d, 'z')
		}
		d = ShortenPathData(d, pathDataBuffer)

		t.Data = pathBytes
		replacee.Text = dBytes
		replacee.AttrVal = d
	}
}

////////////////////////////////////////////////////////////////

func skipTag(tb *TokenBuffer, tag svg.Hash) {
	for {
		if t := *tb.Shift(); (t.TokenType == xml.EndTagToken || t.TokenType == xml.StartTagCloseVoidToken) && t.Hash == tag || t.TokenType == xml.ErrorToken {
			break
		}
	}
}
