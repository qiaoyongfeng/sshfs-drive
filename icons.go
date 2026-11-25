package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"

	"github.com/egoist/mygo/ui"
)

// stroke builds a 24×24 line icon: MyGo colors it with the text color, so the
// SVG itself has no color of its own.
func stroke(body string) *ui.SVG {
	const head = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
		`stroke="#000000" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">`
	return ui.MustParseSVG([]byte(head + body + `</svg>`))
}

var (
	iconPlus   = stroke(`<path d="M12 5v14M5 12h14"/>`)
	iconServer = stroke(`<path d="M17.5 19H7a4 4 0 0 1-.6-7.96A6 6 0 0 1 18 10.5a4.25 4.25 0 0 1-.5 8.5z"/>`)
	iconPlug   = stroke(`<path d="M9 3v5M15 3v5M6 8h12v3a6 6 0 0 1-12 0z"/><path d="M12 17v4"/>`)
	iconFolder = stroke(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2.5h8a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	iconDots   = stroke(`<circle cx="5" cy="12" r="1.4" fill="#000000"/><circle cx="12" cy="12" r="1.4" fill="#000000"/><circle cx="19" cy="12" r="1.4" fill="#000000"/>`)
	iconTrash  = stroke(`<path d="M4 7h16M10 7V5h4v2M6 7l1 13h10l1-13"/><path d="M10 11v5M14 11v5"/>`)
	iconCopy   = stroke(`<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M15 9V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h3"/>`)
	iconPencil = stroke(`<path d="M4 20h4l10-10a2.5 2.5 0 0 0-3.5-3.5L4.5 16.5z"/><path d="M13.5 7.5L16.5 10.5"/>`)
	iconGear   = stroke(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-2.9 1.2 2 2 0 1 1-4 0 1.7 1.7 0 0 0-2.9-1.2l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.7 1.7 0 0 0 3 15a2 2 0 1 1 0-4 1.7 1.7 0 0 0 1.5-2.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.7 1.7 0 0 0 10 3.9 2 2 0 1 1 14 4a1.7 1.7 0 0 0 2.9 1.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1A1.7 1.7 0 0 0 21 11a2 2 0 1 1 0 4 1.7 1.7 0 0 0-1.6 1z"/>`)
	iconInfo   = stroke(`<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>`)
	iconTerm   = stroke(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/>`)
	iconClose  = stroke(`<path d="M6 6l12 12M18 6L6 18"/>`)
	iconGrip   = stroke(`<path d="M9 7h.01M15 7h.01M9 12h.01M15 12h.01M9 17h.01M15 17h.01"/>`)
	iconKey    = stroke(`<circle cx="8" cy="15" r="4"/><path d="M11 12l8-8M17 4l3 3M15 6l3 3"/>`)
	iconAdd    = stroke(`<path d="M12 5v14M5 12h14"/>`)
)

// trayIcon draws the cloud the taskbar shows.
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{R: 0x2b, G: 0x6c, B: 0xdf, A: 0xff}
	disc := func(cx, cy, r float64) {
		for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
			for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
				dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
				if dx*dx+dy*dy <= r*r && x >= 0 && y >= 0 && x < 32 && y < 32 {
					img.Set(x, y, ink)
				}
			}
		}
	}
	disc(11, 20, 7)
	disc(21, 21, 6)
	disc(16, 15, 6)
	for y := 20; y < 26; y++ {
		for x := 6; x < 26; x++ {
			if px := img.NRGBAAt(x, y); px.A == 0 {
				img.Set(x, y, ink)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
