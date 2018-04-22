package app

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fsnotify/fsnotify"

	"navaak/convertor/lib/ffmpeg"
	"navaak/convertor/lib/logger"
	"navaak/convertor/util/file"
)

type application struct {
	config Config
	logger *logger.Logger
}

func New(config Config) (*application, error) {
	a := new(application)
	a.config = config
	runtime.GOMAXPROCS(a.config.MaxUseCPU)
	a.logger = logger.New(a.config.LogPath)
	return a, nil
}

func (a *application) Run() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	done := make(chan bool)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op == fsnotify.Create {
					log.Println("new file detected -- >",
						event.Name)
					a.newVid(event.Name)
				}
			case err := <-watcher.Errors:
				log.Println("error:", err)
			}
		}
	}()
	watchpath, err := filepath.Abs(a.config.WatchPath)
	if err != nil {
		log.Fatal(err)
	}
	println("watching path selected to ---> ", watchpath)
	watcher.Add(watchpath)
	if err != nil {
		return err
	}
	<-done
	return nil
}

func (a *application) newVid(f string) {
	if filepath.Ext(f) != ".mp4" {
		return
	}
	base := filepath.Base(f)
	v, err := ffmpeg.NewVideo(f, a.config.WorkPath,
		ffmpeg.P1080,
		ffmpeg.P720,
		ffmpeg.P480,
		ffmpeg.P360,
		ffmpeg.P240)
	if err != nil {
		go a.logger.Log(base, map[string]string{
			"error": err.Error(),
		})
		return
	}
	v.SetWorkerCount(a.config.MaxUseCPU)
	v.Run()
	loggs := v.Logger()
	go a.logger.Log(base, loggs)
	name := strings.Split(base, ".")[0]
	exportpath := filepath.Join(a.config.ExportPath, name)
	os.MkdirAll(exportpath, 0777)
	orgfile := filepath.Join(exportpath, base)
	if err := file.Move(loggs.SourceFile, orgfile); err != nil {
		log.Fatal(err)
	}
	for _, export := range loggs.Exports {
		base := filepath.Base(export.DestFile)
		dest := filepath.Join(exportpath, base)
		if err := file.Move(export.DestFile, dest); err != nil {
			log.Fatal(err)
		}
	}
	descsfilename := filepath.Join(exportpath, name)
	a.smail(descsfilename, loggs)
	a.json(descsfilename, orgfile, loggs)
}

func (a *application) smail(dest string, logg ffmpeg.Log) {
	dest = dest + ".smail"
	res := smailHead
	for _, ex := range logg.Exports {
		base := filepath.Base(ex.DestFile)
		vid := fmt.Sprintf(smailQualities[ex.Resolution.Height], base)
		res += vid
	}
	res += smailFooter
	if err := ioutil.WriteFile(dest, []byte(res), 0777); err != nil {
		log.Fatal(err)
	}
}

func (a *application) json(dest, org string, logg ffmpeg.Log) {
	dest = dest + ".json"
	id := strings.Split(filepath.Base(org), ".")[0]
	qualities := []int{}
	for _, ex := range logg.Exports {
		qualities = append(qualities, ex.Resolution.Height)
	}
	res := map[string]interface{}{
		"videoId":   id,
		"fullpath":  org,
		"duration":  logg.Duration,
		"size":      logg.Size,
		"qualities": qualities,
	}
	data, _ := json.Marshal(&res)
	if err := ioutil.WriteFile(dest, data, 0777); err != nil {
		log.Fatal(err)
	}
}
