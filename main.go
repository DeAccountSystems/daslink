package main

import (
	"context"
	"daslink/config"
	"daslink/dao"
	"fmt"
	"os"
	"sync"

	"github.com/scorpiotzh/mylog"
	"github.com/scorpiotzh/toolib"
	"github.com/urfave/cli/v2"
)

var (
	log               = mylog.NewLogger("main", mylog.LevelDebug)
	exit              = make(chan struct{})
	ctxServer, cancel = context.WithCancel(context.Background())
	wgServer          = sync.WaitGroup{}
)

func main() {
	log.Debugf("server start:")
	app := &cli.App{
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Load configuration from `FILE`",
			},
		},
		Action: runServer,
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func runServer(ctx *cli.Context) error {
	// config
	configFilePath := ctx.String("config")
	if err := config.InitCfg(configFilePath); err != nil {
		return err
	}

	// db
	cfgMysql := config.Cfg.DB.Mysql
	db, err := dao.NewGormDataBase(cfgMysql.Addr, cfgMysql.User, cfgMysql.Password, cfgMysql.DbName, cfgMysql.MaxOpenConn, cfgMysql.MaxIdleConn)
	if err != nil {
		return fmt.Errorf("NewGormDataBase err:%s", err.Error())
	}
	dbDao := dao.Initialize(db, cfgMysql.LogMode)
	log.Info("db ok")

	// dns
	cfgCloudflare := config.Cfg.CloudFlare
	dnsData, err := NewDNSData(cfgCloudflare.ApiKey, cfgCloudflare.ApiEmail, cfgCloudflare.ZoneName, config.Cfg.IPFS.Gateway, config.Cfg.HostName.Suffix)
	if err != nil {
		return fmt.Errorf("NewDNSData err:%s", err.Error())
	}
	log.Info("dns data ok")

	// read all das accounts that has ipfs or ipns record
	ipfsRecordList, _ := dbDao.FindRecordInfoByKeys([]string{"ipfs", "ipns"})
	jobsChan := make(chan string, len(ipfsRecordList))

	runWatcher(&wgServer, dbDao, ipfsRecordList[len(ipfsRecordList)-1].Id, jobsChan)
	log.Info("Watching new ipfs records...")

	runSyncIpfsRecords(ipfsRecordList, dnsData, jobsChan)
	log.Info("All ipfs records have been synchronized")

	runWorker(&wgServer, dbDao, dnsData, jobsChan)
	log.Info("Worker started")

	// quit monitor
	toolib.ExitMonitoring(func(sig os.Signal) {
		log.Warn("ExitMonitoring:", sig.String())
		cancel()
		log.Warn("Wait for worker to finish...")
		wgServer.Wait()
		exit <- struct{}{}
	})

	<-exit
	log.Warn("success exit server. bye bye!")
	return nil
}