package app

import (
	"image"
	"image/color"
	"path/filepath"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func (a *ChatApp) previewOverlay(gtx layout.Context) {
	for a.closeBtn.Clicked(gtx) {
		a.mu.Lock()
		a.enlarged = ""
		a.preview = previewState{}
		a.mu.Unlock()
		a.win.Invalidate()
	}
	a.mu.Lock()
	path := a.enlarged
	if path != "" && a.preview.path != path {
		a.preview = previewState{path: path, zoom: 1, mode: "fit"}
	}
	a.mu.Unlock()
	if path == "" {
		return
	}
	img, imgOp, err := a.cachedImageOp(path)
	if err != nil {
		a.setStatus("Preview error: " + err.Error())
		return
	}

	a.handlePreviewControls(gtx)
	viewport := gtx.Constraints.Max
	defer clip.Rect{Max: viewport}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 0, G: 0, B: 0, A: 176})
	area := clip.Rect{Max: viewport}.Push(gtx.Ops)
	event.Op(gtx.Ops, &a.preview.tag)
	area.Pop()
	pointer.CursorGrab.Add(gtx.Ops)

	a.layoutZoomableImage(gtx, img, imgOp)
	a.previewToolbar(gtx, filepath.Base(path))
}

func (a *ChatApp) previewToolbar(gtx layout.Context, filename string) layout.Dimensions {
	return layout.S.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Bottom: unit.Dp(34)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(material.Button(a.th, &a.zoomOutBtn, "Zoom Out").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
							layout.Rigid(material.Button(a.th, &a.zoomInBtn, "Zoom In").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
							layout.Rigid(material.Button(a.th, &a.actualBtn, "1:1").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
							layout.Rigid(material.Button(a.th, &a.fitBtn, "Fit").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(8)}.Layout(gtx) }),
							layout.Rigid(material.Button(a.th, &a.closeBtn, "Close").Layout),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Max.X = minInt(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(420)))
							txt := material.Body2(a.th, filename)
							txt.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 245}
							return txt.Layout(gtx)
						})
					}),
				)
			})
		})
	})
}

func (a *ChatApp) handlePreviewControls(gtx layout.Context) {
	for a.zoomInBtn.Clicked(gtx) {
		a.mu.Lock()
		a.preview.zoom = clampFloat32(a.preview.zoom*1.25, 0.2, 8)
		a.preview.mode = "custom"
		a.mu.Unlock()
		a.win.Invalidate()
	}
	for a.zoomOutBtn.Clicked(gtx) {
		a.mu.Lock()
		a.preview.zoom = clampFloat32(a.preview.zoom/1.25, 0.2, 8)
		a.preview.mode = "custom"
		a.mu.Unlock()
		a.win.Invalidate()
	}
	for a.actualBtn.Clicked(gtx) {
		a.mu.Lock()
		a.preview.zoom = 1
		a.preview.mode = "actual"
		a.preview.offset = f32.Point{}
		a.mu.Unlock()
		a.win.Invalidate()
	}
	for a.fitBtn.Clicked(gtx) {
		a.mu.Lock()
		a.preview.zoom = 1
		a.preview.mode = "fit"
		a.preview.offset = f32.Point{}
		a.mu.Unlock()
		a.win.Invalidate()
	}
	a.updatePreviewDrag(gtx)
}

func (a *ChatApp) updatePreviewDrag(gtx layout.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	if a.preview.zoom <= 0 {
		a.preview.zoom = 1
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &a.preview.tag,
			Kinds:  pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Buttons == pointer.ButtonPrimary || e.Source == pointer.Touch {
				a.preview.dragging = true
				a.preview.lastPos = e.Position
				gtx.Execute(pointer.GrabCmd{Tag: &a.preview.tag, ID: e.PointerID})
			}
		case pointer.Drag:
			if a.preview.dragging {
				d := e.Position.Sub(a.preview.lastPos)
				a.preview.offset = a.preview.offset.Add(d)
				a.preview.lastPos = e.Position
				changed = true
			}
		case pointer.Release, pointer.Cancel:
			a.preview.dragging = false
		}
	}
	if changed {
		a.win.Invalidate()
	}
}

func (a *ChatApp) layoutZoomableImage(gtx layout.Context, img image.Image, imgOp paint.ImageOp) layout.Dimensions {
	a.mu.Lock()
	zoom := a.preview.zoom
	offset := a.preview.offset
	mode := a.preview.mode
	a.mu.Unlock()
	if zoom <= 0 {
		zoom = 1
	}
	viewport := gtx.Constraints.Max
	src := img.Bounds().Size()
	if src.X <= 0 || src.Y <= 0 || viewport.X <= 0 || viewport.Y <= 0 {
		return layout.Dimensions{Size: viewport}
	}
	base := minFloat32(float32(viewport.X)/float32(src.X), float32(viewport.Y)/float32(src.Y))
	if base > 1 {
		base = 1
	}
	scale := base * zoom
	if mode == "actual" {
		scale = zoom
	}
	drawSize := image.Pt(maxInt(1, int(float32(src.X)*scale)), maxInt(1, int(float32(src.Y)*scale)))
	center := f32.Pt(float32(viewport.X-drawSize.X)/2, float32(viewport.Y-drawSize.Y)/2)
	pos := image.Pt(int(center.X+offset.X), int(center.Y+offset.Y))

	defer clip.Rect{Max: viewport}.Push(gtx.Ops).Pop()
	defer op.Offset(pos).Push(gtx.Ops).Pop()
	gtx.Constraints.Min = drawSize
	gtx.Constraints.Max = drawSize
	widget.Image{
		Src:   imgOp,
		Fit:   widget.Fill,
		Scale: 1 / gtx.Metric.PxPerDp / scale,
	}.Layout(gtx)
	return layout.Dimensions{Size: viewport}
}

func clampFloat32(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
