package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strconv"

	envConfig "github.com/oldfritter/goDCE/config"
	"github.com/oldfritter/goDCE/initializers"
	"github.com/oldfritter/goDCE/schedules/backup/tasks"
	"github.com/oldfritter/goDCE/schedules/kLine"
	"github.com/oldfritter/goDCE/schedules/order"
	"github.com/oldfritter/goDCE/utils"
	"github.com/robfig/cron"
)

var QueueName string

func main() {
	envConfig.InitEnv()
	utils.InitMainDB()
	utils.InitBackupDB()
	utils.InitRedisPools()
	utils.InitializeAmqpConfig()

	initializers.InitCacheData()

	InitSchedule()

	err := ioutil.WriteFile("pids/schedule.pid", []byte(strconv.Itoa(os.Getpid())), 0644)
	if err != nil {
		fmt.Println(err)
	}

	go func() {
		channel, err := utils.RabbitMqConnect.Channel()
		if err != nil {
			fmt.Errorf("Channel: %s", err)
		}
		channel.ExchangeDeclare("panama.fanout", "fanout", true, false, false, false, nil)
		queue, err := channel.QueueDeclare("", true, false, false, false, nil)
		if err != nil {
			return
		}
		QueueName = queue.Name
		channel.QueueBind(queue.Name, QueueName, "panama.fanout", false, nil)
		msgs, _ := channel.Consume(queue.Name, "", true, false, false, false, nil)
		for _ = range msgs {
			initializers.InitCacheData()
		}
		return
	}()

	quit := make(chan os.Signal)
	signal.Notify(quit, os.Interrupt)
	<-quit
	channel, err := utils.RabbitMqConnect.Channel()
	if err != nil {
		fmt.Errorf("Channel: %s", err)
	}
	channel.QueueDelete(QueueName, false, false, false)
	closeResource()
}

func closeResource() {
	utils.CloseAmqpConnection()
	utils.CloseRedisPools()
	utils.CloseMainDB()
	utils.CloseBackupDB()
}

func InitSchedule() {
	c := cron.New()
	// 日志备份
	c.AddFunc("0 55 23 * * *", tasks.BackupLogFiles)
	c.AddFunc("0 56 23 * * *", tasks.UploadLogFileToS3)
	c.AddFunc("0 59 23 * * *", tasks.CleanLogs)

	for _, schedule := range envConfig.CurrentEnv.Schedules {
		if schedule == "CleanTokens" {
			// 清理tokens
			c.AddFunc("0 57 23 * * *", tasks.CleanTokens)
		} else if schedule == "CreateLatestKLine" {
			// 生成存储于数据库的K线
			c.AddFunc("*/5 * * * * *", kLine.CreateLatestKLine)
		} else if schedule == "WaitingOrderCheck" {
			// 二十秒检查一次待成交订单
			c.AddFunc("*/20 * * * * *", order.WaitingOrderCheck)
		}
	}

	c.Start()
}
