package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func solid(side int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestDownscaleKeepsASolidColourExactly(t *testing.T) {
	want := color.NRGBA{R: 254, G: 109, B: 23, A: 255}
	// 1254 is the real source size and is not a multiple of 16, so this
	// exercises the fractional-overlap weights rather than a clean 2x2 average.
	got, err := Downscale(solid(1254, want), 16)
	if err != nil {
		t.Fatalf("Downscale: %v", err)
	}
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if c := got.NRGBAAt(x, y); c != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, c, want)
			}
		}
	}
}

func TestDownscaleAveragesEachBlock(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := uint8(0)
			if x < 2 { // left half black, right half white, both opaque
				v = 0
			} else {
				v = 255
			}
			src.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	got, err := Downscale(src, 2)
	if err != nil {
		t.Fatalf("Downscale: %v", err)
	}
	if c := got.NRGBAAt(0, 0); c != (color.NRGBA{0, 0, 0, 255}) {
		t.Errorf("left block = %v, want opaque black", c)
	}
	if c := got.NRGBAAt(1, 1); c != (color.NRGBA{255, 255, 255, 255}) {
		t.Errorf("right block = %v, want opaque white", c)
	}
}

// A transparent pixel still carries RGB in an NRGBA image. Averaging that in
// naively would tint every soft edge of the icon; alpha-weighting must ignore it.
func TestDownscaleDoesNotLetTransparentPixelsTintTheEdge(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1+1))
	src.SetNRGBA(0, 0, color.NRGBA{200, 0, 0, 255}) // opaque red
	src.SetNRGBA(1, 0, color.NRGBA{0, 0, 255, 0})   // invisible, blue underneath
	src.SetNRGBA(0, 1, color.NRGBA{200, 0, 0, 255})
	src.SetNRGBA(1, 1, color.NRGBA{0, 0, 255, 0})
	got, err := Downscale(src, 1)
	if err != nil {
		t.Fatalf("Downscale: %v", err)
	}
	c := got.NRGBAAt(0, 0)
	if c.R != 200 || c.G != 0 || c.B != 0 {
		t.Errorf("colour = %v, want the red side only (R=200 G=0 B=0)", c)
	}
	if c.A != 128 { // half the area is opaque: (255*2 + 2) / 4
		t.Errorf("alpha = %d, want 128", c.A)
	}
}

func TestDownscaleLeavesFullyTransparentAreasZeroed(t *testing.T) {
	got, err := Downscale(solid(8, color.NRGBA{12, 34, 56, 0}), 2)
	if err != nil {
		t.Fatalf("Downscale: %v", err)
	}
	if c := got.NRGBAAt(1, 1); c != (color.NRGBA{}) {
		t.Errorf("transparent pixel = %v, want all zero so no stray RGB is embedded", c)
	}
}

func TestDownscaleRejectsUnusableSources(t *testing.T) {
	if _, err := Downscale(image.NewNRGBA(image.Rect(0, 0, 8, 4)), 2); err == nil {
		t.Error("expected an error for a non-square source")
	}
	if _, err := Downscale(solid(8, color.NRGBA{A: 255}), 16); err == nil {
		t.Error("expected an error for upscaling")
	}
	if _, err := Downscale(solid(8, color.NRGBA{A: 255}), 0); err == nil {
		t.Error("expected an error for a zero size")
	}
}

func TestEntriesAreDeterministic(t *testing.T) {
	src := solid(64, color.NRGBA{9, 39, 28, 255})
	first, err := Entries(src, WindowsSizes[:4])
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	second, _ := Entries(src, WindowsSizes[:4])
	for i := range first {
		if !bytes.Equal(first[i].DIB, second[i].DIB) {
			t.Errorf("size %d differs between identical runs", first[i].Size)
		}
	}
}

func TestDIBLayoutMatchesWhatWindowsReads(t *testing.T) {
	const size = 16
	src := image.NewNRGBA(image.Rect(0, 0, size, size))
	src.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})           // top-left
	src.SetNRGBA(size-1, size-1, color.NRGBA{R: 40, G: 50, B: 60, A: 128}) // bottom-right
	entries, err := Entries(src, []int{size})
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	dib := entries[0].DIB

	if got := binary.LittleEndian.Uint32(dib[0:]); got != 40 {
		t.Errorf("biSize = %d, want 40", got)
	}
	if got := int32(binary.LittleEndian.Uint32(dib[4:])); got != size {
		t.Errorf("biWidth = %d, want %d", got, size)
	}
	if got := int32(binary.LittleEndian.Uint32(dib[8:])); got != size*2 {
		t.Errorf("biHeight = %d, want %d (XOR bitmap + AND mask)", got, size*2)
	}
	if got := binary.LittleEndian.Uint16(dib[14:]); got != 32 {
		t.Errorf("biBitCount = %d, want 32", got)
	}
	// 16px: 16*16*4 pixel bytes + a 4-byte-aligned 1-bit mask row per line.
	if want := 40 + size*size*4 + 4*size; len(dib) != want {
		t.Fatalf("payload is %d bytes, want %d", len(dib), want)
	}

	pixels := dib[40:]
	// Rows are stored bottom-up, so the image's bottom-right pixel comes first
	// in the row that is written first, and the top-left pixel starts the last.
	bottomRight := pixels[(size-1)*4 : size*4]
	if !bytes.Equal(bottomRight, []byte{60, 50, 40, 128}) {
		t.Errorf("bottom-right BGRA = %v, want [60 50 40 128]", bottomRight)
	}
	topLeft := pixels[(size-1)*size*4:][:4]
	if !bytes.Equal(topLeft, []byte{30, 20, 10, 255}) {
		t.Errorf("top-left BGRA = %v, want [30 20 10 255]", topLeft)
	}
	if mask := dib[40+size*size*4:]; !bytes.Equal(mask, make([]byte, 4*size)) {
		t.Error("AND mask is not all zero; alpha should be the only transparency")
	}
}

func TestICOHasAConsistentDirectory(t *testing.T) {
	entries, err := Entries(solid(300, color.NRGBA{A: 255}), []int{16, 48, 256})
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	ico := ICO(entries)

	if binary.LittleEndian.Uint16(ico[0:]) != 0 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		t.Error("header is not reserved=0, type=1 (icon)")
	}
	if got := binary.LittleEndian.Uint16(ico[4:]); got != 3 {
		t.Fatalf("image count = %d, want 3", got)
	}
	wantDims := []byte{16, 48, 0} // 256 is stored as 0
	for i, entry := range entries {
		dir := ico[6+16*i:]
		if dir[0] != wantDims[i] || dir[1] != wantDims[i] {
			t.Errorf("entry %d dimensions = %dx%d, want %d", i, dir[0], dir[1], wantDims[i])
		}
		length := binary.LittleEndian.Uint32(dir[8:])
		offset := binary.LittleEndian.Uint32(dir[12:])
		if int(length) != len(entry.DIB) {
			t.Errorf("entry %d length = %d, want %d", i, length, len(entry.DIB))
		}
		if !bytes.Equal(ico[offset:offset+length], entry.DIB) {
			t.Errorf("entry %d does not point at its own image data", i)
		}
	}
}

func TestGroupIconNumbersItsResources(t *testing.T) {
	entries, err := Entries(solid(64, color.NRGBA{A: 255}), []int{16, 32, 64})
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	group := GroupIcon(entries, 5)
	if got := binary.LittleEndian.Uint16(group[4:]); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
	for i, entry := range entries {
		dir := group[6+14*i:]
		if id := binary.LittleEndian.Uint16(dir[12:]); id != uint16(5+i) {
			t.Errorf("entry %d resource ID = %d, want %d", i, id, 5+i)
		}
		if length := binary.LittleEndian.Uint32(dir[8:]); int(length) != len(entry.DIB) {
			t.Errorf("entry %d length = %d, want %d", i, length, len(entry.DIB))
		}
	}
	if want := 6 + 14*3; len(group) != want {
		t.Errorf("group is %d bytes, want %d (GRPICONDIRENTRY is 14 bytes, not the ICO's 16)", len(group), want)
	}
}
