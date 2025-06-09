package main

import (
	"bytes"
	"context"
	"github.com/ipfs/go-cid"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var runCmd = &cli.Command{
	Name:    "run",
	Usage:   "run process",
	Aliases: []string{"r"},
	Action:  runCopy,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:     "root-car-dir",
			Usage:    "root-car-dir",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "copy-script",
			Usage:    "file path of copy-script",
			Required: true,
		},
		&cli.IntFlag{
			Name:  "parallel",
			Usage: "parallel count",
			Value: 16,
		},
	},
}

type CarFileInfo struct {
	Name     string
	Path     string
	PieceCid cid.Cid
	ModTime  time.Time
}

// CreateCar creates a txcar
func runCopy(c *cli.Context) error {
	ctx := c.Context
	rootCarDir := c.String("root-car-dir")
	copyScriptPath := c.String("copy-script")
	parallel := c.Int("parallel")

	m, err := NewCarCopyManager(rootCarDir, copyScriptPath, parallel)
	if err != nil {
		return xerrors.Errorf("%w", err)
	}

	ctx, ctxClose := context.WithCancel(ctx)
	defer ctxClose()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-sigCh:
			log.Warnw("received shutdown", "signal", sig)
			ctxClose()
		case <-ctx.Done():
			log.Warnw("ctx.Done()", "err", ctx.Err())
		}
	}()

	return m.Run(ctx)
}

type CarCopyManager struct {
	rootCarDir     string
	copyScriptPath string
	parallel       int

	parallelThrottle chan struct{}
	runningJobs      map[cid.Cid]CarFileInfo
	runningJobsLock  sync.RWMutex
}

func NewCarCopyManager(rootCarDir string, copyScriptPath string, parallel int) (*CarCopyManager, error) {
	{
		fi, err := os.Stat(rootCarDir)
		if err != nil {
			return nil, xerrors.Errorf("%w", err)
		}
		if !fi.IsDir() {
			return nil, xerrors.Errorf("rootCarDir is not dir: %s", rootCarDir)
		}
	}
	{
		fi, err := os.Stat(copyScriptPath)
		if err != nil {
			return nil, xerrors.Errorf("%w", err)
		}
		if fi.IsDir() {
			return nil, xerrors.Errorf("copyScriptPath is dir: %s", copyScriptPath)
		}
	}

	m := CarCopyManager{
		rootCarDir:     rootCarDir,
		copyScriptPath: copyScriptPath,
		parallel:       parallel,

		parallelThrottle: make(chan struct{}, parallel+1),
		runningJobs:      map[cid.Cid]CarFileInfo{},
	}

	return &m, nil
}

func (m *CarCopyManager) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			log.Warnf("ctx.Done: %v", ctx.Err())
			return context.Cause(ctx)
		default:

		}

		m.runningJobsLock.RLock()
		runningJobCount := len(m.runningJobs)
		m.runningJobsLock.RUnlock()

		log.Infow("runningJobs", "runningJobCount", runningJobCount)
		if runningJobCount >= m.parallel {
			log.Infow("running job count exceeded, so just wait", "runningJobCount", runningJobCount, "max-parallel", m.parallel)
			time.Sleep(time.Second * 20)
			continue
		}

		carFiles, err := getSortedCarFiles(m.rootCarDir)
		if err != nil {
			return xerrors.Errorf("%w", err)
		}
		carFileCount := len(carFiles)
		log.Infow("getSortedCarFiles", "file-count", carFileCount, "parallel", m.parallel)

		//
		var newJobs []CarFileInfo
		m.runningJobsLock.RLock()
		for _, carFile := range carFiles {
			if _, ok := m.runningJobs[carFile.PieceCid]; ok {
				continue
			}
			newJobs = append(newJobs, carFile)
		}
		m.runningJobsLock.RUnlock()

		// do job
		for _, newJob := range newJobs {
			log.Warnw("try to add new job", "carFile", newJob)
			go func(carFile *CarFileInfo) {
				err = m.processJob(ctx, carFile)
				if err != nil {
					log.Errorw("processJob fail", "err", err)
				}
			}(&newJob)
		}
	}
}

func (m *CarCopyManager) processJob(ctx context.Context, carFile *CarFileInfo) error {
	select {
	case <-ctx.Done():
		log.Warnf("ctx.Done: %v", ctx.Err())
		return context.Cause(ctx)
	case m.parallelThrottle <- struct{}{}:
	}
	defer func() {
		select {
		case <-ctx.Done():
			log.Warnf("ctx.Done: %v", ctx.Err())
		case <-m.parallelThrottle:
		}
	}()

	log.Infow("processing Job", "carFile", carFile.Path)

	//
	m.runningJobsLock.Lock()
	m.runningJobs[carFile.PieceCid] = *carFile
	m.runningJobsLock.Unlock()

	defer func() {
		m.runningJobsLock.Lock()
		delete(m.runningJobs, carFile.PieceCid)
		m.runningJobsLock.Unlock()
	}()

	cmd := exec.Command(m.copyScriptPath, carFile.Path)

	log.Infow("try run copy cmd.", "cmd", cmd.String(), "carFile", carFile)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return xerrors.Errorf("exec %s %s. (stderr: %s): %w", m.copyScriptPath, carFile.Path, strings.TrimSpace(errOut.String()), err)
	}
	log.Infow("end run copy cmd.", "cmd", cmd.String(), "carFile", carFile)

	//
	log.Infow("waitCarFileRemoved", "carFile", carFile.Path)
	err := m.waitCarFileRemoved(ctx, carFile)
	if err != nil {
		return xerrors.Errorf("%w", err)
	}

	log.Infow("end processing Job", "carFile", carFile.Path)
	return nil
}

func (m *CarCopyManager) waitCarFileRemoved(ctx context.Context, carFile *CarFileInfo) error {
	for {
		select {
		case <-ctx.Done():
			log.Warnf("ctx.Done: %v", ctx.Err())
			return context.Cause(ctx)
		default:
		}

		_, err := os.Stat(carFile.Path)
		if os.IsNotExist(err) {
			return nil
		}
		time.Sleep(time.Second * 10)
	}
}

func getSortedCarFiles(rootCarDir string) ([]CarFileInfo, error) {
	dirEntries, err := os.ReadDir(rootCarDir)
	if err != nil {
		return nil, xerrors.Errorf("Error reading directory:%s, err:%w", rootCarDir, err)
	}

	var batchDirInfos []os.FileInfo
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			return nil, xerrors.Errorf("Error get file info:%s, err:%w", entry.Name(), err)
		}

		batchDirInfos = append(batchDirInfos, fi)
	}
	sort.Slice(batchDirInfos, func(i, j int) bool {
		return batchDirInfos[i].ModTime().Before(batchDirInfos[j].ModTime())
	})

	var carFiles []CarFileInfo
	for _, batchDirInfo := range batchDirInfos {
		batchDir := filepath.Join(rootCarDir, batchDirInfo.Name())
		log.Infow("process batch", "batch", batchDir)
		carEntries, err := os.ReadDir(batchDir)
		if err != nil {
			return nil, xerrors.Errorf("Error reading directory:%s, err:%w", batchDir, err)
		}

		for _, entry := range carEntries {
			if entry.IsDir() {
				continue
			}
			if filepath.Ext(entry.Name()) != ".car" {
				continue
			}
			if strings.Contains(entry.Name(), "-") {
				continue
			}

			pieceCidStr := strings.TrimSuffix(entry.Name(), ".car")
			pieceCid, err := cid.Parse(pieceCidStr)
			if err != nil {
				log.Errorw("file name is not valid cid", "pieceCidStr", pieceCidStr, "err", err)
				continue
			}

			fi, err := entry.Info()
			if err != nil {
				return nil, xerrors.Errorf("Error get file info:%s, err:%w", entry.Name(), err)
			}

			filePath := filepath.Join(rootCarDir, batchDirInfo.Name(), entry.Name())
			carFile := CarFileInfo{
				Name:     entry.Name(),
				Path:     filePath,
				PieceCid: pieceCid,
				ModTime:  fi.ModTime(),
			}
			carFiles = append(carFiles, carFile)
		}
	}

	sort.Slice(carFiles, func(i, j int) bool {
		return carFiles[i].ModTime.Before(carFiles[j].ModTime)
	})

	return carFiles, nil
}
