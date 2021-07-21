package comm

import (
	"github.com/gomodule/redigo/redis"
	"time"
	"util/logger"
)

type RedisServer struct {
	redisHost string
	redisAuth string
}

//NewRedis ...
func NewRedis(redisHost, redisAuth string) *RedisServer {
	logger.Info("---------------------------redis")
	logger.Info(redisHost)
	logger.Info(redisAuth)
	return &RedisServer{redisHost, redisAuth}
}

func (rs *RedisServer) NewPool() *redis.Pool {
	return &redis.Pool{
		MaxIdle:     5,
		MaxActive:   5,
		IdleTimeout: 30 * time.Second,
		Dial: func() (redis.Conn, error) {

			conn, err := redis.Dial("tcp", rs.redisHost)
			if err != nil {
				return nil, err
			}
			if _, err := conn.Do("AUTH", rs.redisAuth); err != nil {
				conn.Close()
				return nil, err
			}
			return conn, err
		},
		TestOnBorrow: func(conn redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := conn.Do("PING")
			if err != nil {
				return err
			}
			return err
		},
	}
}

func (rs *RedisServer) ListenChannel(pool *redis.Pool, key string, message chan redis.Message) {
	for {
		err := rs.Listen(pool, key, message)
		if err != nil {
			time.Sleep(5 * time.Second)
		}
	}
}

func (rs *RedisServer) Listen(pool *redis.Pool, key string, message chan redis.Message) error {
	var err error
	logger.Info(pool.Stats(), pool.ActiveCount(), pool.IdleCount())
	var psc redis.PubSubConn
	var conn redis.Conn
	conn = pool.Get()
	if conn == nil || err != nil {
		logger.Error("Dial redis err %v", err)
		return err
	}
	psc = redis.PubSubConn{Conn: conn}
	err = psc.Subscribe(key)
	if err != nil {
		logger.Error("subscribe redis %s err %v", key, err)
		return err
	}
	for {
		switch v := psc.ReceiveWithTimeout(30 * time.Minute).(type) {
		case redis.Message: //有消息Publish到指定Channel时
			message <- v
		case redis.Subscription: //Subscribe一个Channel时
			logger.Info("%s: %s %d\n", v.Channel, v.Kind, v.Count)
		default:
			logger.Info("default: %v", v)
		case error:
			time.Sleep(3 * time.Second)
			logger.Info("receive err msg %v", v.Error())
			logger.Info(pool.Stats(), pool.ActiveCount(), pool.IdleCount())
			err = conn.Close()
			if err != nil {
				logger.Error(err)
			}
			conn = pool.Get()
			if conn == nil {
				logger.Error("get redis conn err %v", err)
			}
			psc = redis.PubSubConn{Conn: conn}
			psc.Subscribe(key)
			break
		}
	}
}

func (rs *RedisServer) PublishMsg(pool *redis.Pool, key, value string) {
	conn := pool.Get()
	defer conn.Close()

	_, err := conn.Do("publish", key, value)
	if err != nil {
		logger.Error("redis util send msg err: %v", err)
	}

}
