package rice

import (
	"archive/zip"
	"hash/crc32"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	zipexe "github.com/daaku/go.zipexe"
)

// appendedBox defines an appended box
type appendedBox struct {
	Name  string                   // box name
	Files map[string]*appendedFile // appended files (*zip.File) by full path
	Time  time.Time
}

type appendedFile struct {
	zipFile  *zip.File
	dir      bool
	dirInfo  *appendedDirInfo
	children []*appendedFile
	content  []byte
}

// appendedBoxes is a public register of appendes boxes
var appendedBoxes = make(map[string]*appendedBox)

func init() {
	// find if exec is appended
	//thisFile := "C:\\Go\\projects\\compras\\compras.exe" the test file forget it
	thisFile, err := os.Executable()
	if err != nil {
		return // not appended or cant find self executable
	}

	thisFile, err = filepath.EvalSymlinks(thisFile)
	if err != nil {
		return
	}
	closer, rd, err := zipexe.OpenCloser(thisFile)
	if err != nil {
		return // not appended
	}
	defer closer.Close()

	for _, f := range rd.File {
		// get box and file name from f.Name
		fileParts := strings.SplitN(strings.TrimLeft(filepath.ToSlash(f.Name), "/"), "/", 2)
		boxName := fileParts[0]
		var fileName string
		if len(fileParts) > 1 {
			fileName = fileParts[1]
		}

		// find box or create new one if doesn't exist
		box := appendedBoxes[boxName]
		if box == nil {
			box = &appendedBox{
				Name:  boxName,
				Files: make(map[string]*appendedFile),
				Time:  f.ModTime(),
			}
			appendedBoxes[boxName] = box
		}

		// create and add file to box
		af := &appendedFile{
			zipFile: f,
		}
		if f.Comment == "dir" {
			af.dir = true
			af.dirInfo = &appendedDirInfo{
				name: filepath.Base(af.zipFile.Name),
				time: af.zipFile.ModTime(),
			}
		} else {
			// this is a file, we need it's contents so we can create a bytes.Reader when the file is opened
			// get uncompressed size of the zip file
			var fileSize = af.zipFile.FileInfo().Size()

			// ignore reading empty files from zip (empty file still is a valid file to be read though!)
			if fileSize > 0 {
				// open io.ReadCloser
				rc, err := af.zipFile.Open()
				if err != nil {
					af.content = nil // this will cause an error when the file is being opened or seeked (which is good)
					// TODO: it's quite blunt to just log this stuff. but this is in init, so rice.Debug can't be changed yet..
					log.Printf("error opening appended file %s: %v", af.zipFile.Name, err)
				} else {
					// if you use rc.Read to read the content in a compressed file you will get an EOF error
					// I think there is beacuse a problem of flate implementation
					// ioutil.ReadAll doesn't generate the error and you don't need make the slice
					af.content, err = ioutil.ReadAll(rc)
					rc.Close()

					// to make sure the uncompressed file content is correct we calculate CRC and compare with the store one
					crc1 := crc32.ChecksumIEEE(af.content)
					crc2 := af.zipFile.CRC32

					//compare the two crc and the file size from stored in zip and uncompressed one
					if crc1 != crc2 || fileSize != int64(len(af.content)) {
						if err != nil {
							af.content = nil // this will cause an error when the file is being opened or seeked (which is good)
							// TODO: it's quite blunt to just log this stuff. but this is in init, so rice.Debug can't be changed yet..
							log.Printf("error reading data for appended file %s: %v", af.zipFile.Name, err)
						}
					}
				}
			}
		}

		// add appendedFile to box file list
		box.Files[fileName] = af

		// add to parent dir (if any)
		dirName := filepath.Dir(fileName)
		if dirName == "." {
			dirName = ""
		}
		if fileName != "" { // don't make box root dir a child of itself
			if dir := box.Files[dirName]; dir != nil {
				dir.children = append(dir.children, af)
			}
		}
	}
}

// implements os.FileInfo.
// used for Readdir()
type appendedDirInfo struct {
	name string
	time time.Time
}

func (adi *appendedDirInfo) Name() string {
	return adi.name
}
func (adi *appendedDirInfo) Size() int64 {
	return 0
}
func (adi *appendedDirInfo) Mode() os.FileMode {
	return os.ModeDir
}
func (adi *appendedDirInfo) ModTime() time.Time {
	return adi.time
}
func (adi *appendedDirInfo) IsDir() bool {
	return true
}
func (adi *appendedDirInfo) Sys() interface{} {
	return nil
}
