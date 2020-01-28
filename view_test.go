package goka

import (
	"context"
	"fmt"
	"hash"
	"strconv"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/golang/mock/gomock"
	"github.com/lovoo/goka/codec"
	"github.com/lovoo/goka/logger"
	"github.com/lovoo/goka/storage"
)

var (
	recoveredMessages int
	group             Group  = "group-name"
	topic             string = tableName(group)
)

// constHasher implements a hasher that will always return the specified
// partition. Doesn't properly implement the Hash32 interface, use only in
// tests.
type constHasher struct {
	partition uint32
	returnErr bool
}

func (ch *constHasher) Sum(b []byte) []byte {
	return nil
}

func (ch *constHasher) Sum32() uint32 {
	return ch.partition
}

func (ch *constHasher) BlockSize() int {
	return 0
}

func (ch *constHasher) Reset() {}

func (ch *constHasher) Size() int { return 4 }

func (ch *constHasher) Write(p []byte) (int, error) {
	if ch.returnErr {
		return 0, fmt.Errorf("constHasher write error")
	}
	return len(p), nil
}

func (ch *constHasher) ReturnError() {
	ch.returnErr = true
}

// NewConstHasher creates a constant hasher that hashes any value to 0.
func NewConstHasher(part uint32) *constHasher {
	return &constHasher{partition: part}
}

func createTestView(t *testing.T, consumer sarama.Consumer) (*View, *builderMock, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	bm := newBuilderMock(ctrl)
	recoveredMessages = 0
	opts := &voptions{
		log:        logger.Default(),
		tableCodec: new(codec.String),
		updateCallback: func(s storage.Storage, partition int32, key string, value []byte) error {
			if err := DefaultUpdate(s, partition, key, value); err != nil {
				return err
			}
			recoveredMessages++
			return nil
		},
		hasher: DefaultHasher(),
	}
	opts.builders.storage = bm.getStorageBuilder()
	opts.builders.topicmgr = bm.getTopicManagerBuilder()
	opts.builders.consumerSarama = func(brokers []string, clientID string) (sarama.Consumer, error) {
		return consumer, nil
	}

	view := &View{topic: topic, opts: opts}
	return view, bm, ctrl
}

func TestView_hash(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		view.partitions = []*PartitionTable{
			&PartitionTable{},
		}

		h, err := view.hash("a")
		assertNil(t, err)
		assertTrue(t, h == 0)
	})
	t.Run("fail_hash", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		view.partitions = []*PartitionTable{
			&PartitionTable{},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			hasher.ReturnError()
			return hasher
		}

		_, err := view.hash("a")
		assertNotNil(t, err)
	})
	t.Run("fail_no_partition", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		_, err := view.hash("a")
		assertNotNil(t, err)
	})
}

func TestView_find(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			key   string        = "some-key"
			proxy *storageProxy = &storageProxy{}
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			return hasher
		}

		st, err := view.find(key)
		assertNil(t, err)
		assertEqual(t, st, proxy)
	})
	t.Run("fail", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		view.partitions = []*PartitionTable{
			&PartitionTable{},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			hasher.ReturnError()
			return hasher
		}

		_, err := view.find("a")
		assertNotNil(t, err)
	})
}

func TestView_Get(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage:   bm.mst,
				partition: 0,
				update: func(s storage.Storage, partition int32, key string, value []byte) error {
					return nil
				},
			}
			key   string = "some-key"
			value int64  = 3
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.tableCodec = &codec.Int64{}

		bm.mst.EXPECT().Get(key).Return([]byte(strconv.FormatInt(value, 10)), nil)

		ret, err := view.Get(key)
		assertNil(t, err)
		assertTrue(t, ret == value)
	})
	t.Run("succeed_nil", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage:   bm.mst,
				partition: 0,
				update: func(s storage.Storage, partition int32, key string, value []byte) error {
					return nil
				},
			}
			key string = "some-key"
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.tableCodec = &codec.Int64{}

		bm.mst.EXPECT().Get(key).Return(nil, nil)

		ret, err := view.Get(key)
		assertNil(t, err)
		assertTrue(t, ret == nil)
	})
	t.Run("fail_get", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage:   bm.mst,
				partition: 0,
				update: func(s storage.Storage, partition int32, key string, value []byte) error {
					return nil
				},
			}
			key    string = "some-key"
			errRet error  = fmt.Errorf("get failed")
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.tableCodec = &codec.Int64{}

		bm.mst.EXPECT().Get(key).Return(nil, errRet)

		_, err := view.Get(key)
		assertNotNil(t, err)
	})
}

func TestView_Has(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			key string = "some-key"
			has bool   = true
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			return hasher
		}

		bm.mst.EXPECT().Has(key).Return(has, nil)

		ret, err := view.Has(key)
		assertNil(t, err)
		assertEqual(t, ret, has)
	})
	t.Run("succeed_false", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			key string = "some-key"
			has bool   = false
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			return hasher
		}

		bm.mst.EXPECT().Has(key).Return(has, nil)

		ret, err := view.Has(key)
		assertNil(t, err)
		assertEqual(t, ret, has)
	})
	t.Run("fail_err", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			key string = "some-key"
			has bool   = false
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			hasher.ReturnError()
			return hasher
		}

		ret, err := view.Has(key)
		assertNotNil(t, err)
		assertTrue(t, ret == has)
	})
}

func TestView_Evict(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			key   string        = "some-key"
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.hasher = func() hash.Hash32 {
			hasher := NewConstHasher(0)
			return hasher
		}
		bm.mst.EXPECT().Delete(key).Return(nil)

		err := view.Evict(key)
		assertNil(t, err)
	})
}

func TestView_Recovered(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			hasRecovered bool = true
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				state: NewSignal(State(PartitionRunning)).SetState(State(PartitionRunning)),
			},
		}
		ret := view.Recovered()
		assertTrue(t, ret == hasRecovered)
	})
	t.Run("true", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			hasRecovered bool = false
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				state: NewSignal(State(PartitionRunning), State(PartitionRecovering)).SetState(State(PartitionRecovering)),
			},
			&PartitionTable{
				state: NewSignal(State(PartitionRunning)).SetState(State(PartitionRunning)),
			},
		}
		ret := view.Recovered()
		assertTrue(t, ret == hasRecovered)
	})
}

func TestView_Topic(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		ret := view.Topic()
		assertTrue(t, ret == topic)
	})
}

func TestView_close(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
		}
		bm.mst.EXPECT().Close().Return(nil).AnyTimes()

		multiErr := view.close()
		assertNil(t, multiErr.NilOrError())
		assertTrue(t, len(view.partitions) == 0)
	})
	t.Run("fail", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			retErr error = fmt.Errorf("some-error")
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
		}
		bm.mst.EXPECT().Close().Return(retErr).AnyTimes()

		multiErr := view.close()
		assertNotNil(t, multiErr.NilOrError())
		assertTrue(t, len(view.partitions) == 0)
	})
}

func TestView_Terminate(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			isRestartable bool = true
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.restartable = isRestartable
		bm.mst.EXPECT().Close().Return(nil).AnyTimes()

		ret := view.Terminate()
		assertNil(t, ret)
		assertTrue(t, len(view.partitions) == 0)
		assertTrue(t, view.terminated == true)
	})
	t.Run("succeed_twice", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			isRestartable bool = true
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.restartable = isRestartable
		bm.mst.EXPECT().Close().Return(nil).AnyTimes()

		ret := view.Terminate()
		assertNil(t, ret)
		assertTrue(t, len(view.partitions) == 0)
		assertTrue(t, view.terminated == true)
		ret = view.Terminate()
		assertNil(t, ret)
		assertTrue(t, len(view.partitions) == 0)
		assertTrue(t, view.terminated == true)
	})
	t.Run("succeed_not_restartable", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			isRestartable bool = false
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.restartable = isRestartable

		ret := view.Terminate()
		assertNil(t, ret)
		assertTrue(t, len(view.partitions) == 3)
		assertTrue(t, view.terminated == false)
	})
	t.Run("fail", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			proxy *storageProxy = &storageProxy{
				Storage: bm.mst,
			}
			retErr        error = fmt.Errorf("some-error")
			isRestartable bool  = true
		)
		view.partitions = []*PartitionTable{
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
			&PartitionTable{
				st: proxy,
			},
		}
		view.opts.restartable = isRestartable
		bm.mst.EXPECT().Close().Return(retErr).AnyTimes()

		ret := view.Terminate()
		assertNotNil(t, ret)
		assertTrue(t, len(view.partitions) == 0)
	})
}

func TestView_Run(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			oldest int64 = 0
			newest int64 = 10
			// local     int64          = oldest
			consumer  *MockAutoConsumer = defaultSaramaAutoConsumerMock(t)
			partition int32             = 0
			count     int64             = 0
			updateCB  UpdateCallback    = func(s storage.Storage, partition int32, key string, value []byte) error {
				count++
				return nil
			}
		)
		bm.useMemoryStorage()

		pt := newPartitionTable(
			topic,
			partition,
			consumer,
			bm.tmgr,
			updateCB,
			bm.getStorageBuilder(),
			logger.Default(),
		)

		pt.consumer = consumer
		view.partitions = []*PartitionTable{pt}
		view.state = NewSignal(State(ViewStateCatchUp), State(ViewStateRunning), State(ViewStateIdle)).SetState(State(ViewStateIdle))

		bm.tmgr.EXPECT().GetOffset(pt.topic, pt.partition, sarama.OffsetOldest).Return(oldest, nil).AnyTimes()
		bm.tmgr.EXPECT().GetOffset(pt.topic, pt.partition, sarama.OffsetNewest).Return(newest, nil).AnyTimes()
		partConsumer := consumer.ExpectConsumePartition(topic, partition, AnyOffset)
		for i := 0; i < 10; i++ {
			partConsumer.YieldMessage(&sarama.ConsumerMessage{})
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				if count == 10 {
					time.Sleep(time.Millisecond * 10)
					cancel()
					return
				}
			}
		}()

		ret := view.Run(ctx)
		assertNil(t, ret)
	})
	t.Run("fail", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			partition int32             = 0
			consumer  *MockAutoConsumer = defaultSaramaAutoConsumerMock(t)
			updateCB  UpdateCallback    = nil
			retErr    error             = fmt.Errorf("run error")
		)
		bm.useMemoryStorage()

		pt := newPartitionTable(
			topic,
			partition,
			consumer,
			bm.tmgr,
			updateCB,
			bm.getStorageBuilder(),
			logger.Default(),
		)

		pt.consumer = consumer
		view.partitions = []*PartitionTable{pt}
		view.state = NewSignal(State(ViewStateCatchUp), State(ViewStateRunning), State(ViewStateIdle)).SetState(State(ViewStateIdle))

		bm.mst.EXPECT().GetOffset(gomock.Any()).Return(int64(0), retErr).AnyTimes()
		bm.tmgr.EXPECT().GetOffset(pt.topic, pt.partition, sarama.OffsetNewest).Return(sarama.OffsetNewest, retErr).AnyTimes()
		bm.tmgr.EXPECT().GetOffset(pt.topic, pt.partition, sarama.OffsetOldest).Return(sarama.OffsetOldest, retErr).AnyTimes()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		ret := view.Run(ctx)
		assertNotNil(t, ret)
	})
}

func TestView_createPartitions(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			partition int32 = 0
		)
		bm.tmgr.EXPECT().Partitions(topic).Return([]int32{partition}, nil)
		bm.tmgr.EXPECT().Close()

		ret := view.createPartitions([]string{""})
		assertNil(t, ret)
		assertTrue(t, len(view.partitions) == 1)
	})
	t.Run("fail_tmgr", func(t *testing.T) {
		view, bm, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		var (
			retErr error = fmt.Errorf("tmgr-partition-error")
		)
		bm.tmgr.EXPECT().Partitions(topic).Return(nil, retErr)
		bm.tmgr.EXPECT().Close()

		ret := view.createPartitions([]string{""})
		assertNotNil(t, ret)
		assertTrue(t, len(view.partitions) == 0)
	})
}

func TestView_WaitRunning(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		view, _, ctrl := createTestView(t, NewMockAutoConsumer(t, DefaultConfig()))
		defer ctrl.Finish()

		view.state = NewSignal(State(ViewStateCatchUp), State(ViewStateRunning), State(ViewStateIdle)).SetState(State(ViewStateRunning))

		var isRunning bool
		select {
		case <-view.WaitRunning():
			isRunning = true
		case <-time.After(time.Second):
		}

		assertTrue(t, isRunning == true)
	})
}

func TestView_NewView(t *testing.T) {
	t.Run("succeed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		bm := newBuilderMock(ctrl)

		var (
			partition int32 = 0
		)
		bm.tmgr.EXPECT().Partitions(topic).Return([]int32{partition}, nil).AnyTimes()
		bm.tmgr.EXPECT().Close().AnyTimes()

		view, err := NewView([]string{""}, Table(topic), &codec.Int64{}, []ViewOption{
			WithViewTopicManagerBuilder(bm.getTopicManagerBuilder()),
			WithViewConsumerSaramaBuilder(func(brokers []string, clientID string) (sarama.Consumer, error) {
				return NewMockAutoConsumer(t, DefaultConfig()), nil
			}),
		}...)
		assertNil(t, err)
		assertNotNil(t, view)
	})
	t.Run("succeed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		bm := newBuilderMock(ctrl)

		var (
			retErr error = fmt.Errorf("tmgr-error")
		)
		bm.tmgr.EXPECT().Partitions(topic).Return(nil, retErr).AnyTimes()
		bm.tmgr.EXPECT().Close().AnyTimes()

		view, err := NewView([]string{""}, Table(topic), &codec.Int64{}, []ViewOption{
			WithViewTopicManagerBuilder(bm.getTopicManagerBuilder()),
			WithViewConsumerSaramaBuilder(func(brokers []string, clientID string) (sarama.Consumer, error) {
				return NewMockAutoConsumer(t, DefaultConfig()), nil
			}),
		}...)
		assertNotNil(t, err)
		assertNil(t, view)
	})
}
