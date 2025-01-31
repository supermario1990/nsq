package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/judwhite/go-svc"
	"github.com/mreiferson/go-options"
	"github.com/nsqio/nsq/internal/lg"
	"github.com/nsqio/nsq/internal/version"
	"github.com/nsqio/nsq/nsqd"
)

type program struct {
	once sync.Once
	nsqd *nsqd.NSQD
}

func main() {
	prg := &program{}
	// SIGTERM handling is in Start()
	if err := svc.Run(prg, syscall.SIGINT); err != nil {
		logFatal("%s", err)
	}
}

func (p *program) Init(env svc.Environment) error {
	if env.IsWindowsService() {
		dir := filepath.Dir(os.Args[0])
		return os.Chdir(dir)
	}
	return nil
}

func (p *program) Start() error {
	opts := nsqd.NewOptions()

	flagSet := nsqdFlagSet(opts)
	flagSet.Parse(os.Args[1:])

	rand.Seed(time.Now().UTC().UnixNano())

	if flagSet.Lookup("version").Value.(flag.Getter).Get().(bool) {
		fmt.Println(version.String("nsqd"))
		os.Exit(0)
	}

	var cfg config
	configFile := flagSet.Lookup("config").Value.String()
	if configFile != "" {
		_, err := toml.DecodeFile(configFile, &cfg)
		if err != nil {
			logFatal("failed to load config file %s - %s", configFile, err)
		}
	}
	cfg.Validate()

	options.Resolve(opts, flagSet, cfg)

	nsqd, err := nsqd.New(opts)
	if err != nil {
		logFatal("failed to instantiate nsqd - %s", err)
	}
	if p.nsqd != nil{
		ctx, ctxCancel := p.nsqd.GetCtx()
		nsqd.SetCtx(ctx, ctxCancel)
	}
	p.nsqd = nsqd

	err = p.nsqd.LoadMetadata()
	if err != nil {
		logFatal("failed to load metadata - %s", err)
	}
	err = p.nsqd.PersistMetadata()
	if err != nil {
		logFatal("failed to persist metadata - %s", err)
	}

	signalChan := make(chan os.Signal, 1)
	go func() {
		// range over all term signals
		// we don't want to un-register our sigterm handler which would
		// cause default go behavior to apply
		for range signalChan {
			p.once.Do(func() {
				p.nsqd.Exit()
			})
		}
	}()
	signal.Notify(signalChan, syscall.SIGTERM)

	go func() {
		err := p.nsqd.Main()
		if err != nil {
			p.Stop()
			os.Exit(1)
		}
	}()

	return nil
}

func (p *program) Stop() error {
	p.once.Do(func() {
		p.nsqd.Exit()
	})
	return nil
}

// Context returns a context that will be canceled when nsqd initiates the shutdown
func (p *program) Context() context.Context {
	if p.nsqd == nil{
		p.nsqd = &nsqd.NSQD{}
		ctx, ctxCancel := context.WithCancel(context.Background())
		p.nsqd.SetCtx(ctx, ctxCancel)
	}
	return p.nsqd.Context()
}

func logFatal(f string, args ...interface{}) {
	lg.LogFatal("[nsqd] ", f, args...)
}
