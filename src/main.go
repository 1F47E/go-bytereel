package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/schollz/progressbar/v3"
)

// Amount of bits in 1 4k frame
// 3840*2160/4 = 2073600
const frameSizeBits = 2073600

var wg sync.WaitGroup

func main() {
	// read cmd line args
	args := os.Args[1:]
	if len(args) == 0 {
		log.Fatal("No file given")
	}
	file := args[0]

	// get file extension via filePath
	ext := filepath.Ext(file)
	if ext == ".png" {
		decode(file)
	} else {
		encode(file)
	}

	wg.Wait()
}

func decode(filename string) {
	fmt.Println("Decoding")

	// read the image
	file, err := os.Open(filename)
	if err != nil {
		log.Fatal("Cannot open file:", err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		log.Fatal("Cannot decode file:", err)
	}

	bits := make([]bool, 0)

	// copy image to bytes
	// TODO: read all 4 pixels and decide if black or white on majority
	for x := 0; x < img.Bounds().Dx(); x += 2 {
		for y := 0; y < img.Bounds().Dy(); y += 2 {
			// get color of pixel
			col := img.At(x, y)
			r, g, b, _ := col.RGBA()

			// white = {255 255 255 255}
			// black = {0 0 0 255}
			isBlack := r == 0 && g == 0 && b == 0
			isWhite := r == 0xFFFF && g == 0xFFFF && b == 0xFFFF

			if isBlack {
				bits = append(bits, true)
			} else if isWhite {
				bits = append(bits, false)
			}
		}
		// fmt.Println("bits len:", len(bits))
		// fmt.Println("bits:", bits)
	}
	// convert bits to bytes
	bytes := make([]byte, len(bits)/8)
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			if bits[i+j] {
				b |= 1 << uint(j)
			}
		}
		bytes[i/8] = b
	}

	// write bytes to file
	ext := filepath.Ext(filename)
	outputFilename := fmt.Sprintf("decoded%s", ext)
	file, err = os.Create(outputFilename)
	if err != nil {
		log.Fatal("Cannot create file:", err)
	}
	defer file.Close()
	_, err = file.Write(bytes)
	if err != nil {
		log.Fatal("Cannot write to file:", err)
	}
	log.Println("Done")

}

func encode(filename string) {

	// open a file and read to bytes
	file, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// TODO: stream file data, not copy to buffer
	var buf bytes.Buffer
	_, err = io.Copy(&buf, file)
	if err != nil {
		log.Fatal(err)
	}

	b := buf.Bytes()

	// print the size
	fmt.Print("File size: ")
	if len(b) > 1024 {
		fmt.Printf("%d %s\n", len(b)/1024, "KB")
	} else if len(b) > 1024*1024 {
		fmt.Printf("%d %s\n", len(b)/1024/1024, "MB")
	} else if len(b) > 1024*1024*1024 {
		fmt.Printf("%d %s\n", len(b)/1024/1024/1024, "GB")
	} else {
		fmt.Printf("%d %s\n", len(b), "Bytes")
	}

	// calc amount of frames and frame size
	totalFramesCnt := uint64(math.Ceil(float64(len(b)) / float64(frameSizeBits/8)))
	fmt.Println("Frames:", totalFramesCnt)
	totalFrameBytes := int(totalFramesCnt) * frameSizeBits / 8
	// fmt.Println("Frames bytes size:", totalFrameBytes)
	digits := int(math.Log10(float64(totalFramesCnt))) + 1 // Calculate number of digits
	// fmt.Println("Digits:", digits)

	// init progress bar
	bar := progressbar.NewOptions(int(totalFramesCnt),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionSetDescription("Encoding..."),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}))

	bitIndex := 0
	var bitsBuffer [frameSizeBits]bool
	// range over all frames, more then file len!
	for i := 0; i < totalFrameBytes; i++ {
		// for every byte, range over all bits
		for j := 0; j < 8; j++ {
			frameNumber := i / (frameSizeBits / 8)
			shift := frameNumber * frameSizeBits
			bitIndex = i*8 + j - shift // should reset to 0 on every frame
			// if we have more bytes than needed, fill the rest with 0
			if i >= len(b) {
				bitsBuffer[bitIndex] = false
			} else {
				bitsBuffer[bitIndex] = (b[i] & (1 << uint(j))) != 0
			}

			// detect the end of the file or the end of the frame
			// proccess the image, save, reset the buffer
			// send a copy of bits buffer to goroutine to proccess
			// panic on errors - missed frames are not allowed
			if bitIndex == len(bitsBuffer)-1 || bitIndex == len(b)*8-1 {
				// create filename
				// prefix filename with dynamic leading zeroes
				fileName := fmt.Sprintf("tmp/out_%0"+strconv.Itoa(digits)+"d.png", frameNumber)

				wg.Add(1)
				go func(bitsBuffer [frameSizeBits]bool, fn string) {
					defer wg.Done()
					// fmt.Println("Proccessing frame in G:", fn)
					img := encodeFrame(bitsBuffer)
					save(fileName, img)
					// fmt.Println("Frame done:", fn)
					bar.Add(1)
				}(bitsBuffer, fileName)
			}
		}
	}
}

func encodeFrame(bits [frameSizeBits]bool) *image.NRGBA {
	// fmt.Println("Encoding frame")

	// generate an image
	img := image.NewNRGBA(image.Rect(0, 0, 3840, 2160)) // 4K resolution

	// generate image
	// fmt.Println("filling the image")
	k := 0
	for x := 0; x < img.Bounds().Dx(); x += 2 {
		for y := 0; y < img.Bounds().Dy(); y += 2 {
			// var col color.Color
			// set red color as default background
			col := color.NRGBA{255, 0, 0, 255}
			if k < len(bits) { // BUG: always true
				if bits[k] {
					// col = color.Black
					col = color.NRGBA{0, 0, 0, 255}
				} else {
					// col = color.White
					col = color.NRGBA{255, 255, 255, 255}
				}
				k++
			} else {
				col = color.NRGBA{255, 0, 0, 255}
				fmt.Println("END")
			}
			// Set a 2x2 block of pixels to the color.
			img.Set(x, y, col)
			img.Set(x+1, y, col)
			img.Set(x, y+1, col)
			img.Set(x+1, y+1, col)
		}
	}

	// fmt.Println("Encoding frame done")
	return img
}

func save(filePath string, img *image.NRGBA) {
	imgFile, err := os.Create(filePath)
	defer imgFile.Close()
	if err != nil {
		log.Println("Cannot create file:", err)
		panic(fmt.Sprintf("Cannot create file: %s", err))
	}
	err = png.Encode(imgFile, img.SubImage(img.Rect))
	if err != nil {
		log.Println("Cannot encode to file:", err)
		panic(fmt.Sprintf("Cannot encode to file: %s", err))
	}
}
