package main

import (
	"fmt"
	"github.com/zekrotja/rogu/log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/ranna-go/ranna/internal/api"
	"github.com/ranna-go/ranna/internal/config"
	"github.com/ranna-go/ranna/internal/file"
	"github.com/ranna-go/ranna/internal/namespace"
	"github.com/ranna-go/ranna/internal/sandbox"
	"github.com/ranna-go/ranna/internal/sandbox/docker"
	"github.com/ranna-go/ranna/internal/scheduler"
	"github.com/ranna-go/ranna/internal/spec"
)

type ConfigProvider interface {
	Config() *config.Config
}

type Scheduler interface {
	Schedule(spec interface{}, job func()) (id interface{}, err error)
}

type Manager interface {
	PrepareEnvironments(force bool) []error
}

type SpecProvider interface {
	Spec() *spec.SafeSpecMap
	Load() error
}

func checkErr(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "initialization failed: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	godotenv.Load()

	cfg := config.NewPaerser("")
	err := cfg.Load()
	checkErr(err)

	log.SetLevel(cfg.Config().Log.Level)

	if cfg.Config().Sandbox.EnableNetworking {
		log.Warn().Msg("ATTENTION: Sandbox Networking is enabled by config! This is a high security risk!")
	}

	specFile := cfg.Config().SpecFile
	var specProvider SpecProvider
	if strings.HasPrefix(specFile, "https://") || strings.HasPrefix(specFile, "http://") {
		specProvider = spec.NewHttpProvider(specFile)
	} else {
		specProvider = spec.NewFileProvider(specFile)
	}
	err = specProvider.Load()
	checkErr(err)

	sandboxProvider, err := docker.NewProvider(cfg)
	checkErr(err)

	fileProvider := file.NewLocalFileProvider()

	namespaceProvider := namespace.NewRandomProvider()

	sandboxManager, err := sandbox.NewManager(sandboxProvider, specProvider, fileProvider, cfg, namespaceProvider)
	checkErr(err)
	defer func() {
		log.Info().Msg("cleaning up running sandboxes ...")
		// TODO: Handle errors
		sandboxManager.Cleanup()
	}()

	webApi, err := api.NewRestAPI(cfg, specProvider, sandboxManager)
	checkErr(err)

	schedulerProvider := scheduler.NewCronScheduler()
	schedulerProvider.Start()
	defer schedulerProvider.Stop()

	if !cfg.Config().SkipStartupPrep {
		log.Info().Msg("Prepare spec environments ...")
		// TODO: Handle errors
		sandboxManager.PrepareEnvironments(true)
	} else {
		log.Warn().Msg("Skipping spec preparation on startup")
	}

	if err := scheduleTasks(cfg, schedulerProvider, sandboxManager, specProvider); err != nil {
		log.Fatal().Err(err).Msg("failed scheduling job")
	}

	go func() {
		err = webApi.ListenAndServeBlocking()
		checkErr(err)
	}()

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}

func scheduleTasks(
	cfg ConfigProvider,
	sched Scheduler,
	mgr Manager,
	specProvider SpecProvider,
) (err error) {
	schedule := func(name, spec string, job func()) (err error) {
		if spec != "" {
			log.Info().Field("name", name).Field("spec", spec).Msg("Scheduling job")
			_, err = sched.Schedule(spec, job)
		}
		return
	}

	scheduleSpec := cfg.Config().Scheduler.UpdateImages
	if err = schedule("update spec environments", scheduleSpec, func() {
		log.Info().Msg("Updating spec environments ...")
		defer log.Info().Msg("Updating spec finished")
		mgr.PrepareEnvironments(true)
	}); err != nil {
		return
	}

	scheduleSpec = cfg.Config().Scheduler.UpdateSpecs
	if err = schedule("update specs", scheduleSpec, func() {
		if err = specProvider.Load(); err != nil {
			log.Error().Err(err).Msg("Failed loading specs")
		} else {
			log.Info().Msg("Specs updated")
		}
	}); err != nil {
		return
	}

	return
}
