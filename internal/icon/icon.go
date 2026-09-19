// Package icon renders the SpoolSmith product icon at the sizes Windows and the
// product site need, using only the standard library.
//
// Everything here is deterministic on purpose. The renders are embedded in the
// committed rsrc_windows_amd64.syso files, and a drift test regenerates them
// and compares bytes, so the output must not depend on the Go version, the CPU
// or floating-point behaviour. That is why resampling is integer arithmetic and
// why every size is stored as an uncompressed DIB: image/png's compressor is
// free to change between Go releases, a raw bitmap cannot.
package icon

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
)

// WindowsSizes are the sizes embedded in the executables: the shell's small,
// medium, large and extra-large views, plus the 256px tile Explorer uses for
// "extra large icons".
var WindowsSizes = []int{16, 24, 32, 48, 64, 256}

// FaviconSizes are the sizes in the site's favicon.ico. It leaves out 256px so
// the file browsers download stays a few kilobytes.
var FaviconSizes = []int{16, 32, 48}

// Entry is one size of the icon, already encoded as the DIB payload an ICO file
// or an RT_ICON resource carries.
type Entry struct {
	Size int
	DIB  []byte
}

// Downscale shrinks a square source to size x size pixels by exact area
// averaging, weighting colour by alpha so a translucent edge does not bleed
// its hidden RGB into the result. Only shrinking is supported: upscaling a
// product icon is a sign the wrong source was supplied.
func Downscale(src image.Image, size int) (*image.NRGBA, error) {
	bounds := src.Bounds()
	side := bounds.Dx()
	if side != bounds.Dy() {
		return nil, fmt.Errorf("icon: source is %dx%d, want a square image", bounds.Dx(), bounds.Dy())
	}
	if size <= 0 || size > side {
		return nil, fmt.Errorf("icon: cannot render %dpx from a %dpx source", size, side)
	}

	pixels := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(pixels, pixels.Bounds(), src, bounds.Min, draw.Src)

	// A destination pixel covers side/size source pixels. Measuring both grids
	// in units of 1/(side*size) keeps every overlap an exact integer.
	type sums struct{ a, r, g, b uint64 }

	// Horizontal pass: side rows, size columns.
	rows := make([]sums, side*size)
	for y := 0; y < side; y++ {
		for x := 0; x < size; x++ {
			lo, hi := x*side, (x+1)*side
			var total sums
			for i := lo / size; i <= (hi-1)/size; i++ {
				weight := uint64(min(hi, (i+1)*size) - max(lo, i*size))
				offset := pixels.PixOffset(i, y)
				alpha := uint64(pixels.Pix[offset+3])
				total.a += alpha * weight
				total.r += uint64(pixels.Pix[offset]) * alpha * weight
				total.g += uint64(pixels.Pix[offset+1]) * alpha * weight
				total.b += uint64(pixels.Pix[offset+2]) * alpha * weight
			}
			rows[y*size+x] = total
		}
	}

	// Vertical pass over the horizontal sums.
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	area := uint64(side) * uint64(side)
	for y := 0; y < size; y++ {
		lo, hi := y*side, (y+1)*side
		for x := 0; x < size; x++ {
			var total sums
			for j := lo / size; j <= (hi-1)/size; j++ {
				weight := uint64(min(hi, (j+1)*size) - max(lo, j*size))
				row := rows[j*size+x]
				total.a += row.a * weight
				total.r += row.r * weight
				total.g += row.g * weight
				total.b += row.b * weight
			}
			if total.a == 0 {
				continue // fully transparent: leave the pixel zeroed
			}
			offset := out.PixOffset(x, y)
			out.Pix[offset] = uint8((total.r + total.a/2) / total.a)
			out.Pix[offset+1] = uint8((total.g + total.a/2) / total.a)
			out.Pix[offset+2] = uint8((total.b + total.a/2) / total.a)
			out.Pix[offset+3] = uint8((total.a + area/2) / area)
		}
	}
	return out, nil
}

// Entries renders src at each requested size.
func Entries(src image.Image, sizes []int) ([]Entry, error) {
	entries := make([]Entry, 0, len(sizes))
	for _, size := range sizes {
		scaled, err := Downscale(src, size)
		if err != nil {
			return nil, err
		}
		entries = append(entries, Entry{Size: size, DIB: encodeDIB(scaled)})
	}
	return entries, nil
}

// encodeDIB produces the image payload used by both ICO files and RT_ICON
// resources: a BITMAPINFOHEADER, bottom-up 32-bit BGRA pixels, and the 1-bit
// AND mask. Transparency lives in the alpha channel, so the mask is all zero,
// but it must still be present and correctly sized.
func encodeDIB(img *image.NRGBA) []byte {
	size := img.Bounds().Dx()
	maskStride := (size + 31) / 32 * 4
	imageBytes := size*size*4 + maskStride*size

	var buf bytes.Buffer
	buf.Grow(40 + imageBytes)
	put(&buf, uint32(40))         // biSize
	put(&buf, int32(size))        // biWidth
	put(&buf, int32(size*2))      // biHeight: XOR bitmap plus AND mask
	put(&buf, uint16(1))          // biPlanes
	put(&buf, uint16(32))         // biBitCount
	put(&buf, uint32(0))          // biCompression: BI_RGB
	put(&buf, uint32(imageBytes)) // biSizeImage
	put(&buf, int32(0))           // biXPelsPerMeter
	put(&buf, int32(0))           // biYPelsPerMeter
	put(&buf, uint32(0))          // biClrUsed
	put(&buf, uint32(0))          // biClrImportant

	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			offset := img.PixOffset(x, y)
			buf.Write([]byte{img.Pix[offset+2], img.Pix[offset+1], img.Pix[offset], img.Pix[offset+3]})
		}
	}
	buf.Write(make([]byte, maskStride*size))
	return buf.Bytes()
}

// dimensionByte is the width/height byte of an icon directory entry, where 256
// is stored as 0.
func dimensionByte(size int) byte {
	if size >= 256 {
		return 0
	}
	return byte(size)
}

// ICO assembles entries into a standalone .ico file.
func ICO(entries []Entry) []byte {
	var buf bytes.Buffer
	put(&buf, uint16(0))            // reserved
	put(&buf, uint16(1))            // type: icon
	put(&buf, uint16(len(entries))) // count
	offset := uint32(6 + 16*len(entries))
	for _, entry := range entries {
		buf.WriteByte(dimensionByte(entry.Size)) // width
		buf.WriteByte(dimensionByte(entry.Size)) // height
		buf.WriteByte(0)                         // palette colours
		buf.WriteByte(0)                         // reserved
		put(&buf, uint16(1))                     // planes
		put(&buf, uint16(32))                    // bits per pixel
		put(&buf, uint32(len(entry.DIB)))        // bytes in resource
		put(&buf, offset)                        // offset of the image
		offset += uint32(len(entry.DIB))
	}
	for _, entry := range entries {
		buf.Write(entry.DIB)
	}
	return buf.Bytes()
}

// GroupIcon builds the RT_GROUP_ICON payload for entries stored as RT_ICON
// resources numbered firstID, firstID+1, and so on. It is the ICO directory
// with each file offset replaced by that resource ID.
func GroupIcon(entries []Entry, firstID uint16) []byte {
	var buf bytes.Buffer
	put(&buf, uint16(0))            // reserved
	put(&buf, uint16(1))            // type: icon
	put(&buf, uint16(len(entries))) // count
	for i, entry := range entries {
		buf.WriteByte(dimensionByte(entry.Size)) // width
		buf.WriteByte(dimensionByte(entry.Size)) // height
		buf.WriteByte(0)                         // palette colours
		buf.WriteByte(0)                         // reserved
		put(&buf, uint16(1))                     // planes
		put(&buf, uint16(32))                    // bits per pixel
		put(&buf, uint32(len(entry.DIB)))        // bytes in resource
		put(&buf, firstID+uint16(i))             // RT_ICON resource ID
	}
	return buf.Bytes()
}

func put(buf *bytes.Buffer, value any) {
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		// Fixed-size values into an in-memory buffer cannot fail.
		panic(fmt.Sprintf("icon: encoding %T: %v", value, err))
	}
}
