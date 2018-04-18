package ffmpeg

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"navaak/convertor/lib/ffprobe"
)

type Video struct {
	src            string
	destDir        string
	scales         []string
	details        *ffprobe.FileDetail
	worker         int
	exports        []*Export
	sourceDuration time.Duration
}

type Export struct {
	dest           string
	resolution     ffprobe.Resolution
	err            error
	progress       float32
	sourceDuration time.Duration
	scale          string
	done           bool
}

func NewVideo(src, destDir string, scales ...string) (*Video, error) {
	details, err := ffprobe.GetDetail(src)
	if err != nil {
		return nil, err
	}
	v := new(Video)
	v.src = src
	v.destDir = destDir
	v.details = details
	v.sourceDuration = strDurationToTime(v.details.Format.Duration)
	for _, scale := range scales {
		if err := v.newExp(scale); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (v *Video) Run() {
	go func() {
		if v.worker < 1 {
			v.SetWorkerCount(2)
		}
		var (
			job            sync.WaitGroup
			jobsDoingCount int
		)
		for _, export := range v.exports {
			job.Add(1)
			jobsDoingCount++
			go v.exec(export, &job)
			if jobsDoingCount >= v.worker {
				job.Wait()
				jobsDoingCount = 0
			}
		}
	}()
}

func (v *Video) Progress() chan float32 {
	p := make(chan float32)
	go v.calculateProgress(p)
	return p
}

func (v *Video) JobsCount() int {
	return len(v.exports)
}

func (v *Video) SetWorkerCount(n int) {
	v.worker = n
}

func (v *Video) Wait() {
	p := v.Progress()
	<-p
}

func (v *Video) Logs() {
}

func (v *Video) calculateProgress(p chan float32) {
	defer close(p)
	for {
		time.Sleep(time.Second)
		var (
			sum    float32
			onWork float32
		)

		for _, ex := range v.exports {
			if ex.err != nil {
				continue
			}
			onWork++
			sum += ex.progress
		}
		progress := sum / onWork
		if progress >= 100 {
			p <- 100
			break
		}
		p <- progress
	}
}

func (v *Video) newExp(scale string) error {
	resolution, ok := scales[scale]
	if !ok {
		return errors.New("ffmpeg: " + scale + " is undefined")
	}
	dest, err := v.makeFilepath(scale)
	if err != nil {
		return err
	}
	if resolution.Height > v.details.Resolution.Height ||
		resolution.Width > v.details.Resolution.Width {
		return nil
	}
	e := new(Export)
	e.dest = dest
	e.resolution = resolution
	e.scale = scale
	v.exports = append(v.exports, e)
	e.sourceDuration = v.sourceDuration
	return nil
}

func (v *Video) exec(e *Export, job *sync.WaitGroup) {
	defer job.Done()
	scale := fmt.Sprintf("scale=%d:%d",
		e.resolution.Width, e.resolution.Height)
	cmd := exec.Command("ffmpeg", "-y", "-i",
		v.src, "-vf", scale,
		"-codec:v", "libx264",
		"-preset", "slow",
		"-b:v", scalesBV[e.scale],
		"-b:a", scalesBA[e.scale],
		"-maxrate", scalesBuffRates[e.scale],
		"-bufsize", scalesBuffRates[e.scale],
		"-profile:v", scalesProfiles[e.scale],
		e.dest)
	stdout, err := cmd.StderrPipe()
	if err != nil {
		e.err = err
		return
	}
	cmd.Start()
	go func() {
		e.readout(stdout)
	}()

	if err := cmd.Wait(); err != nil {
		e.err = err
		return
	}
	e.done = true
}

func (v *Video) makeFilepath(scale string) (string, error) {
	base := filepath.Base(v.src)
	ex := filepath.Ext(v.src)
	splits := strings.Split(base, ex)
	if len(splits) < 2 {
		return "", errors.New("error source file path")
	}
	name := splits[0]
	filename := name + scalesPreExt[scale] + ext
	path := filepath.Join(v.destDir, filename)
	return path, nil
}

func (e *Export) readout(r io.Reader) {
	buf := make([]byte, 1024, 1024)
	counter := 0
	for {
		n, err := r.Read(buf[:])
		counter++
		if counter < 50 {
			continue
		}
		if n > 0 {
			d := buf[:n]
			current := parseDurationFromReader(string(d))
			e.progress = getProgress(current, e.sourceDuration)
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return
		}
	}
}

func getProgress(current, total time.Duration) float32 {
	p := float32(current.Seconds()/total.Seconds()) * 100
	if p >= 100 {
		return 100
	}
	return p
}

func parseDurationFromReader(s string) time.Duration {
	re := regexp.MustCompile("time=([0-9]+):([0-9]+):([0-9]+)")
	submatches := re.FindAllStringSubmatch(s, -1)
	if len(submatches) < 1 {
		return time.Minute * 15
	}
	if len(submatches[0]) < 4 {
		return time.Minute * 15
	}
	hour, _ := strconv.Atoi(submatches[0][1])
	min, _ := strconv.Atoi(submatches[0][2])
	sec, _ := strconv.Atoi(submatches[0][3])
	return time.Duration(int(time.Hour)*hour) +
		time.Duration(int(time.Minute)*min) +
		time.Duration(int(time.Second)*sec)
}

func strDurationToTime(s string) time.Duration {
	n, _ := strconv.ParseFloat(s, 32)
	return time.Duration(int(time.Second) * int(n))
}