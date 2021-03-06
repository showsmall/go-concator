package acceptorFilters

import (
	"fmt"

	"github.com/Laisky/go-concator/libs"
	utils "github.com/Laisky/go-utils"
	"go.uber.org/zap"
)

type DefaultFilterCfg struct {
	RemoveEmptyTag     bool
	RemoveUnsupportTag bool
	tags               map[string]interface{}
}

type DefaultFilter struct {
	*BaseFilter
	*DefaultFilterCfg
}

func NewDefaultFilterCfg() *DefaultFilterCfg {
	fmt.Printf("tag configs: %+v", libs.LoadTagConfigs())
	c := &DefaultFilterCfg{
		RemoveEmptyTag:     true,
		RemoveUnsupportTag: true,
		tags:               map[string]interface{}{},
	}

	for tag := range libs.LoadTagConfigs() {
		c.tags[tag] = nil
	}

	return c
}

func NewDefaultFilter(cfg *DefaultFilterCfg) *DefaultFilter {
	utils.Logger.Info("NewDefaultFilter")

	return &DefaultFilter{
		BaseFilter:       &BaseFilter{},
		DefaultFilterCfg: cfg,
	}
}

func (f *DefaultFilter) IsTagInConfigs(tag string) (ok bool) {
	_, ok = f.tags[tag]
	return ok
}

func (f *DefaultFilter) Filter(msg *libs.FluentMsg) *libs.FluentMsg {
	if f.RemoveEmptyTag && msg.Tag == "" {
		utils.Logger.Debug("remove empty tag", zap.String("tag", msg.Tag))
		f.msgPool.Put(msg)
		return nil
	}

	if f.RemoveUnsupportTag && !f.IsTagInConfigs(msg.Tag) {
		utils.Logger.Debug("remove unsupported tag", zap.String("tag", msg.Tag))
		f.msgPool.Put(msg)
		return nil
	}

	return msg
}
