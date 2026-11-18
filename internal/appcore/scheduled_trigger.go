package appcore

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"syncmaid/internal/taskconfig"
)

var ErrNoScheduledFuture = errors.New("此表达式没有可计算的未来执行时间，请修改计划")
var scheduledParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Five fields only; no descriptor, seconds, year or timezone override. The
// library's local-zone schedule uses the location of the time passed to Next.
func ParseScheduled(expression string, after time.Time) (cron.Schedule, time.Time, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 || strings.Contains(expression, "?") || strings.HasPrefix(fields[0], "TZ=") || strings.HasPrefix(fields[0], "CRON_TZ=") {
		return nil, time.Time{}, errors.New("cron需要五字段：分 时 日 月 星期；本地时区，不支持秒、年份、?或时区前缀")
	}
	schedule, e := scheduledParser.Parse(strings.Join(fields, " "))
	if e != nil {
		return nil, time.Time{}, fmt.Errorf("cron: %w", e)
	}
	next := schedule.Next(after)
	if next.IsZero() {
		return nil, time.Time{}, ErrNoScheduledFuture
	}
	return schedule, next, nil
}

// Normal timer/dispatch jitter is tolerated. A notification delayed by sleep
// for more than five seconds is discarded, never replayed as historical work.
const ScheduledLateness = 5 * time.Second

func ScheduledOnTime(now, due time.Time) bool {
	return !now.Before(due) && now.Sub(due) <= ScheduledLateness
}

func ValidateApplicationTrigger(t taskconfig.Trigger) error {
	if t.HasUnknownFields() {
		return fmt.Errorf("不支持此触发配置")
	}
	if t.Kind == "manual" && t.SettleSeconds == nil && t.CronExpression == nil {
		return nil
	}
	if t.Kind == "watch" && t.SettleSeconds != nil && t.CronExpression == nil {
		return nil
	}
	if t.Kind == "scheduled" && t.CronExpression != nil && t.SettleSeconds == nil {
		return nil
	}
	return fmt.Errorf("不支持或不完整的触发配置")
}
