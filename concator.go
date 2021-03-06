package concator

import (
	"reflect"
	"regexp"
	"sync"
	"time"

	"github.com/Laisky/go-concator/libs"
	utils "github.com/Laisky/go-utils"
	"go.uber.org/zap"
)

// Concator work for one tag, contains many identifier("container_id")
type Concator struct {
	outChan    chan<- *libs.FluentMsg
	msgKey     string // msgKey to concat
	identifier string // key to distinguish sources
	slot       map[string]*PendingMsg
	regex      *regexp.Regexp // regex to match first line
	pMsgPool   *sync.Pool
	maxLen     int
}

// ConcatorFactory can spawn new Concator
type ConcatorFactory struct {
	outChan  chan *libs.FluentMsg
	pMsgPool *sync.Pool
}

// PendingMsg is the message wait tobe concatenate
type PendingMsg struct {
	msg   *libs.FluentMsg
	lastT time.Time
}

// NewConcator create new Concator
func NewConcator(outChan chan<- *libs.FluentMsg, msgKey, identifier string, regex *regexp.Regexp, pMsgPool *sync.Pool) *Concator {
	utils.Logger.Debug("create new concator", zap.String("identifier", identifier), zap.String("msgKey", msgKey))
	return &Concator{
		outChan:    outChan,
		msgKey:     msgKey,
		identifier: identifier,
		regex:      regex,
		slot:       map[string]*PendingMsg{},
		pMsgPool:   pMsgPool,
		maxLen:     utils.Settings.GetInt("settings.max_msg_length"),
	}
}

// Run starting Concator to concatenate messages,
// you should not run concator directly,
// it's better to create and run Concator by ConcatorFactory
//
// TODO: concator for each tag now,
// maybe set one concator for each identifier in the future for better performance
func (c *Concator) Run(inChan <-chan *libs.FluentMsg) {
	var (
		msg               *libs.FluentMsg
		pmsg              *PendingMsg
		identifier        string
		identifierI, msgI interface{}
		ok                bool

		now             time.Time
		initWaitTs      = 20 * time.Millisecond
		maxWaitTs       = 500 * time.Millisecond
		waitTs          = initWaitTs
		nWaits          = 0
		nWaitsToDouble  = 2
		concatTimeoutTs = 5 * time.Second
		timer           = NewTimer(NewTimerConfig(initWaitTs, maxWaitTs, waitTs, concatTimeoutTs, nWaits, nWaitsToDouble))
	)

	for {
		select {
		case msg = <-inChan:
			now = time.Now()
			timer.Reset(now)
			identifierI, ok = msg.Message[c.identifier]
			// messages without identifier
			if !ok {
				utils.Logger.Warn("identifier not exists", zap.String("identifier", c.identifier), zap.String("tag", msg.Tag))
				c.outChan <- msg
				continue
			}

			// unknown identifier type
			switch identifierI.(type) {
			case []byte:
				identifier = string(identifierI.([]byte))
			case string:
				identifier = identifierI.(string)
			default:
				utils.Logger.Error("unknown identifier type, should be `[]byte` or `string`",
					zap.String("type", reflect.TypeOf(identifierI).String()))
				c.outChan <- msg
				continue
			}

			pmsg, ok = c.slot[identifier]
			// new identifier
			if !ok {
				utils.Logger.Debug("got new identifier", zap.String("identifier", identifier), zap.ByteString("log", msg.Message[c.msgKey].([]byte)))
				pmsg = c.pMsgPool.Get().(*PendingMsg)
				pmsg.lastT = now
				pmsg.msg = msg
				c.slot[identifier] = pmsg
				continue
			}

			// old identifer
			msgI, ok = msg.Message[c.msgKey]
			if !ok {
				utils.Logger.Warn("message not exists", zap.String("msgKey", c.msgKey), zap.String("tag", msg.Tag))
				c.outChan <- msg
				continue
			}
			if c.regex.MatchString(string(msgI.([]byte))) { // new line
				utils.Logger.Debug("got new line",
					zap.ByteString("log", msg.Message[c.msgKey].([]byte)),
					zap.String("tag", msg.Tag))
				c.outChan <- c.slot[identifier].msg
				c.slot[identifier].msg = msg
				c.slot[identifier].lastT = now
				continue
			}

			// need to concat
			utils.Logger.Debug("concat lines", zap.ByteString("log", msg.Message[c.msgKey].([]byte)))
			c.slot[identifier].msg.Message[c.msgKey] =
				append(c.slot[identifier].msg.Message[c.msgKey].([]byte), '\n')
			c.slot[identifier].msg.Message[c.msgKey] =
				append(c.slot[identifier].msg.Message[c.msgKey].([]byte), msg.Message[c.msgKey].([]byte)...)
			if c.slot[identifier].msg.ExtIds == nil {
				c.slot[identifier].msg.ExtIds = []int64{} // create ids, wait to append tail-msg's id
			}
			c.slot[identifier].msg.ExtIds = append(c.slot[identifier].msg.ExtIds, msg.Id)
			c.slot[identifier].lastT = now

			// too long to send
			if len(c.slot[identifier].msg.Message[c.msgKey].([]byte)) >= c.maxLen {
				utils.Logger.Debug("too long to send", zap.String("msgKey", c.msgKey), zap.String("tag", msg.Tag))
				c.outChan <- c.slot[identifier].msg
				c.pMsgPool.Put(c.slot[identifier])
				delete(c.slot, identifier)
			}

		default: // check timeout
			now = time.Now()
			for identifier, pmsg = range c.slot {
				if now.Sub(pmsg.lastT) > concatTimeoutTs { // timeout to flush
					utils.Logger.Debug("timeout flush", zap.ByteString("log", pmsg.msg.Message[c.msgKey].([]byte)))
					c.outChan <- pmsg.msg
					c.pMsgPool.Put(pmsg)
					delete(c.slot, identifier)
				}
			}

			timer.Sleep()
		}
	}
}

// NewConcatorFactory create new ConcatorFactory
func NewConcatorFactory() *ConcatorFactory {
	utils.Logger.Info("create concatorFactory")
	outChan := make(chan *libs.FluentMsg, 5000)
	return &ConcatorFactory{
		outChan: outChan,
		pMsgPool: &sync.Pool{
			New: func() interface{} {
				return &PendingMsg{}
			},
		},
	}
}

// Spawn create and run new Concator for new tag
func (cf *ConcatorFactory) Spawn(msgKey, identifier string, regex *regexp.Regexp) chan<- *libs.FluentMsg {
	inChan := make(chan *libs.FluentMsg, 1000)
	concator := NewConcator(cf.outChan, msgKey, identifier, regex, cf.pMsgPool)
	go concator.Run(inChan)
	return inChan
}

// MessageChan return the message chan that collect messages produced by all Concator
func (cf *ConcatorFactory) MessageChan() chan *libs.FluentMsg {
	return cf.outChan
}
