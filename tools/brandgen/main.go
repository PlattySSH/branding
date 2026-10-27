package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

type pt struct{ x, y float64 }

type seg struct {
	op byte
	p  []pt
}

type item struct {
	segs []seg
	col  string
}

type art struct {
	items        []item
	x0, y0, w, h float64
}

const (
	aurora = "#3BD39A"
	night  = "#08130F"
	ink    = "#0B1A14"
	paper  = "#F4F8F5"
	white  = "#FFFFFF"
	black  = "#000000"
)

var out string

func poly(pts ...float64) []seg {
	var s []seg
	for i := 0; i < len(pts); i += 2 {
		op := byte('L')
		if i == 0 {
			op = 'M'
		}
		s = append(s, seg{op, []pt{{pts[i], pts[i+1]}}})
	}
	return append(s, seg{'Z', nil})
}

func markSegs() []seg {
	a := poly(0, 3, 6, 3, 6, 13, 10, 13, 10, 16, 0, 16)
	b := poly(6, 0, 16, 0, 16, 13, 10, 13, 10, 3, 6, 3)
	return append(a, b...)
}

func transform(s []seg, sc, dx, dy float64) []seg {
	r := make([]seg, len(s))
	for i, g := range s {
		ps := make([]pt, len(g.p))
		for j, p := range g.p {
			ps[j] = pt{p.x*sc + dx, p.y*sc + dy}
		}
		r[i] = seg{g.op, ps}
	}
	return r
}

type typeface struct {
	f   *sfnt.Font
	upm float64
	cap float64
}

func loadFont(path string) typeface {
	data, err := os.ReadFile(path)
	check(err)
	f, err := sfnt.Parse(data)
	check(err)
	var b sfnt.Buffer
	upm := float64(f.UnitsPerEm())
	m, err := f.Metrics(&b, fixed.Int26_6(f.UnitsPerEm())<<6, font.HintingNone)
	check(err)
	c := float64(m.CapHeight) / 64
	if c == 0 {
		c = 0.7 * upm
	}
	return typeface{f, upm, c / upm}
}

func fx(v fixed.Int26_6) float64 { return float64(v) / 64 }

func textSegs(t typeface, s string, tracking float64) ([]seg, float64) {
	var b sfnt.Buffer
	ppem := fixed.Int26_6(int(t.upm)) << 6
	sc := 1 / t.upm
	var segs []seg
	x := 0.0
	var prev sfnt.GlyphIndex
	for i, r := range s {
		gi, err := t.f.GlyphIndex(&b, r)
		check(err)
		if i > 0 {
			if k, err := t.f.Kern(&b, prev, gi, ppem, font.HintingNone); err == nil {
				x += fx(k) * sc
			}
		}
		gs, err := t.f.LoadGlyph(&b, gi, ppem, nil)
		check(err)
		open := false
		for _, g := range gs {
			p := func(k int) pt { return pt{fx(g.Args[k].X)*sc + x, fx(g.Args[k].Y) * sc} }
			switch g.Op {
			case sfnt.SegmentOpMoveTo:
				if open {
					segs = append(segs, seg{'Z', nil})
				}
				segs = append(segs, seg{'M', []pt{p(0)}})
				open = true
			case sfnt.SegmentOpLineTo:
				segs = append(segs, seg{'L', []pt{p(0)}})
			case sfnt.SegmentOpQuadTo:
				segs = append(segs, seg{'Q', []pt{p(0), p(1)}})
			case sfnt.SegmentOpCubeTo:
				segs = append(segs, seg{'C', []pt{p(0), p(1), p(2)}})
			}
		}
		if open {
			segs = append(segs, seg{'Z', nil})
		}
		adv, err := t.f.GlyphAdvance(&b, gi, ppem, font.HintingNone)
		check(err)
		x += fx(adv)*sc + tracking
		prev = gi
	}
	return segs, x - tracking
}

func bounds(items []item) (float64, float64, float64, float64) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, it := range items {
		for _, g := range it.segs {
			for _, p := range g.p {
				x0, y0 = math.Min(x0, p.x), math.Min(y0, p.y)
				x1, y1 = math.Max(x1, p.x), math.Max(y1, p.y)
			}
		}
	}
	return x0, y0, x1, y1
}

func newArt(items []item) art {
	x0, y0, x1, y1 := bounds(items)
	return art{items, x0, y0, x1 - x0, y1 - y0}
}

func markArt(col string) art {
	return art{[]item{{markSegs(), col}}, 0, 0, 16, 16}
}

func horizontal(t typeface, markCol, textCol string) art {
	ts, _ := textSegs(t, "Northlight", -0.025)
	ms := t.cap * 1.24
	gap := 0.34
	cy := -t.cap / 2
	m := transform(markSegs(), ms/16, 0, cy-ms/2)
	tx := transform(ts, 1, ms+gap, 0)
	return newArt([]item{{m, markCol}, {tx, textCol}})
}

func stacked(t typeface, markCol, textCol string) art {
	ts, tw := textSegs(t, "Northlight", -0.025)
	ms := t.cap * 2.6
	gap := 0.42
	m := transform(markSegs(), ms/16, (tw-ms)/2, -t.cap-gap-ms)
	return newArt([]item{{m, markCol}, {ts, textCol}})
}

func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*1000)/1000, 'f', -1, 64)
}

func pathData(s []seg, sc, dx, dy float64) string {
	var b strings.Builder
	for _, g := range s {
		b.WriteByte(g.op)
		for i, p := range g.p {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(num(p.x*sc + dx))
			b.WriteByte(' ')
			b.WriteString(num(p.y*sc + dy))
		}
	}
	return b.String()
}

func writeSVG(name string, a art, height float64, bg string) {
	sc := height / a.h
	w, h := a.w*sc, height
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s">`, num(w), num(h), num(w), num(h))
	if bg != "" {
		fmt.Fprintf(&b, `<rect width="%s" height="%s" fill="%s"/>`, num(w), num(h), bg)
	}
	for _, it := range a.items {
		fmt.Fprintf(&b, `<path fill="%s" d="%s"/>`, it.col, pathData(it.segs, sc, -a.x0*sc, -a.y0*sc))
	}
	b.WriteString("</svg>\n")
	save(name, []byte(b.String()))
}

func hex(c string) color.NRGBA {
	v, err := strconv.ParseUint(strings.TrimPrefix(c, "#"), 16, 32)
	check(err)
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

func fill(dst *image.RGBA, s []seg, col string) {
	b := dst.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	z.DrawOp = draw.Over
	var start pt
	for _, g := range s {
		switch g.op {
		case 'M':
			start = g.p[0]
			z.MoveTo(float32(g.p[0].x), float32(g.p[0].y))
		case 'L':
			z.LineTo(float32(g.p[0].x), float32(g.p[0].y))
		case 'Q':
			z.QuadTo(float32(g.p[0].x), float32(g.p[0].y), float32(g.p[1].x), float32(g.p[1].y))
		case 'C':
			z.CubeTo(float32(g.p[0].x), float32(g.p[0].y), float32(g.p[1].x), float32(g.p[1].y), float32(g.p[2].x), float32(g.p[2].y))
		case 'Z':
			z.LineTo(float32(start.x), float32(start.y))
			z.ClosePath()
		}
	}
	z.Draw(dst, b, image.NewUniform(hex(col)), image.Point{})
}

func canvas(w, h int, bg string) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if bg != "" {
		draw.Draw(img, img.Bounds(), image.NewUniform(hex(bg)), image.Point{}, draw.Src)
	}
	return img
}

func place(dst *image.RGBA, a art, x, y, height float64) {
	sc := height / a.h
	for _, it := range a.items {
		fill(dst, transform(it.segs, sc, x-a.x0*sc, y-a.y0*sc), it.col)
	}
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	check(png.Encode(&buf, img))
	return buf.Bytes()
}

func writePNG(name string, img image.Image) { save(name, encode(img)) }

func artPNG(name string, a art, height int, pad float64, bg string) {
	p := pad * float64(height)
	h := float64(height)
	inner := h - 2*p
	w := int(math.Ceil(a.w*inner/a.h + 2*p))
	img := canvas(w, height, bg)
	place(img, a, p, p, inner)
	writePNG(name, img)
}

func roundRect(x, y, w, h, r float64) []seg {
	k := 0.5523 * r
	return []seg{
		{'M', []pt{{x + r, y}}},
		{'L', []pt{{x + w - r, y}}},
		{'C', []pt{{x + w - r + k, y}, {x + w, y + r - k}, {x + w, y + r}}},
		{'L', []pt{{x + w, y + h - r}}},
		{'C', []pt{{x + w, y + h - r + k}, {x + w - r + k, y + h}, {x + w - r, y + h}}},
		{'L', []pt{{x + r, y + h}}},
		{'C', []pt{{x + r - k, y + h}, {x, y + h - r + k}, {x, y + h - r}}},
		{'L', []pt{{x, y + r}}},
		{'C', []pt{{x, y + r - k}, {x + r - k, y}, {x + r, y}}},
		{'Z', nil},
	}
}

func appIcon(n int) *image.RGBA {
	s := float64(n)
	img := canvas(n, n, "")
	fill(img, roundRect(0, 0, s, s, s*0.225), night)
	ms := s * 0.5
	if n <= 24 {
		ms = s * 0.75
	}
	m := ms
	if n <= 32 {
		m = math.Round(ms/16) * 16
		if m == 0 {
			m = ms
		}
	}
	off := math.Round((s - m) / 2)
	fill(img, transform(markSegs(), m/16, off, off), aurora)
	return img
}

func appIconSVG() {
	s := 1024.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1024" height="1024">`)
	fmt.Fprintf(&b, `<path fill="%s" d="%s"/>`, night, pathData(roundRect(0, 0, s, s, s*0.225), 1, 0, 0))
	fmt.Fprintf(&b, `<path fill="%s" d="%s"/>`, aurora, pathData(markSegs(), 32, 256, 256))
	b.WriteString("</svg>\n")
	save("icon/northlight-icon.svg", []byte(b.String()))
}

func writeICO(name string, sizes []int) {
	var imgs [][]byte
	for _, n := range sizes {
		imgs = append(imgs, encode(appIcon(n)))
	}
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, []uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, n := range sizes {
		d := uint8(n)
		if n >= 256 {
			d = 0
		}
		buf.Write([]byte{d, d, 0, 0})
		binary.Write(&buf, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&buf, binary.LittleEndian, []uint32{uint32(len(imgs[i])), uint32(off)})
		off += len(imgs[i])
	}
	for _, d := range imgs {
		buf.Write(d)
	}
	save(name, buf.Bytes())
}

func save(name string, data []byte) {
	p := filepath.Join(out, filepath.FromSlash(name))
	check(os.MkdirAll(filepath.Dir(p), 0o755))
	check(os.WriteFile(p, data, 0o644))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	out = os.Args[2]
	t := loadFont(os.Args[1])

	marks := map[string]string{"aurora": aurora, "white": white, "black": black}
	for name, col := range marks {
		writeSVG("logo/northlight-mark-"+name+".svg", markArt(col), 256, "")
		for _, n := range []int{16, 24, 32, 48, 64, 128, 256, 512, 1024} {
			artPNG(fmt.Sprintf("png/mark/northlight-mark-%s-%d.png", name, n), markArt(col), n, 0, "")
		}
	}

	logos := map[string][2]string{
		"light": {aurora, ink},
		"dark":  {aurora, white},
		"white": {white, white},
		"black": {black, black},
	}
	for name, c := range logos {
		writeSVG("logo/northlight-logo-"+name+".svg", horizontal(t, c[0], c[1]), 96, "")
		writeSVG("logo/northlight-logo-stacked-"+name+".svg", stacked(t, c[0], c[1]), 256, "")
		for _, h := range []int{64, 128, 256} {
			artPNG(fmt.Sprintf("png/logo/northlight-logo-%s-%d.png", name, h), horizontal(t, c[0], c[1]), h, 0, "")
		}
		artPNG(fmt.Sprintf("png/logo/northlight-logo-stacked-%s-512.png", name), stacked(t, c[0], c[1]), 512, 0, "")
	}

	appIconSVG()
	for _, n := range []int{16, 24, 32, 48, 64, 128, 256, 512, 1024} {
		writePNG(fmt.Sprintf("icon/northlight-icon-%d.png", n), appIcon(n))
	}
	writeICO("icon/northlight.ico", []int{16, 24, 32, 48, 64, 128, 256})

	av := canvas(1024, 1024, night)
	fill(av, transform(markSegs(), 512.0/16, 256, 256), aurora)
	writePNG("social/northlight-github-avatar.png", av)

	av2 := canvas(1024, 1024, aurora)
	fill(av2, transform(markSegs(), 512.0/16, 256, 256), night)
	writePNG("social/northlight-github-avatar-aurora.png", av2)

	og := canvas(1200, 630, night)
	lg := horizontal(t, aurora, white)
	lh := 96.0
	lw := lg.w * lh / lg.h
	place(og, lg, (1200-lw)/2, (630-lh)/2, lh)
	writePNG("social/northlight-og-1200x630.png", og)

	bn := canvas(1280, 320, night)
	bh := 72.0
	bw := lg.w * bh / lg.h
	place(bn, lg, (1280-bw)/2, (320-bh)/2, bh)
	writePNG("social/northlight-banner-1280x320.png", bn)

	fmt.Printf("cap=%.4f\n", t.cap)
}
