package proxyd

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/redis/go-redis/v9"
)

func NewRedisClient(url string, enable_cluster bool) (redis.UniversalClient, error) {
	if enable_cluster {
		log.Info("Using cluster redis client.")
		opts, err := redis.ParseClusterURL(url)
		if err != nil {
			return nil, err
		}
		return redis.NewClusterClient(opts), nil
	} else {
		log.Info("Using default redis client.")
		opts, err := redis.ParseURL(url)
		if err != nil {
			return nil, err
		}
		return redis.NewClient(opts), nil
	}
}

func CheckRedisConnection(client redis.UniversalClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return wrapErr(err, "error connecting to redis")
	}

	return nil
}
