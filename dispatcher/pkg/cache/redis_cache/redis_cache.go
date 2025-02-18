//     Copyright (C) 2020-2021, IrineSistiana
//
//     This file is part of mosdns.
//
//     mosdns is free software: you can redistribute it and/or modify
//     it under the terms of the GNU General Public License as published by
//     the Free Software Foundation, either version 3 of the License, or
//     (at your option) any later version.
//
//     mosdns is distributed in the hope that it will be useful,
//     but WITHOUT ANY WARRANTY; without even the implied warranty of
//     MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//     GNU General Public License for more details.
//
//     You should have received a copy of the GNU General Public License
//     along with this program.  If not, see <https://www.gnu.org/licenses/>.

package redis_cache

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/IrineSistiana/mosdns/v2/dispatcher/pkg/pool"
	"github.com/go-redis/redis/v8"
	"github.com/miekg/dns"
	"time"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(url string) (*RedisCache, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	c := redis.NewClient(opt)
	return &RedisCache{client: c}, nil
}

func (r *RedisCache) Get(ctx context.Context, key string, allowExpired bool) (v *dns.Msg, storedTime, expirationTime time.Time, err error) {
	b, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil { // no such key in redis, suppress redis.Nil err.
			return nil, time.Time{}, time.Time{}, nil
		}
		return nil, time.Time{}, time.Time{}, err
	}

	storedTime, expirationTime, m, err := unpackRedisValue(b)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("failed to unpack redis data: %w", err)
	}

	if !allowExpired && expirationTime.Before(time.Now()) { // suppress expired value
		return nil, time.Time{}, time.Time{}, nil
	}

	return m, storedTime, expirationTime, nil
}

func (r *RedisCache) Store(ctx context.Context, key string, v *dns.Msg, storedTime, expirationTime time.Time) (err error) {
	if time.Now().After(expirationTime) {
		return nil
	}

	data, err := packRedisData(storedTime, expirationTime, v)
	if err != nil {
		return err
	}
	defer pool.ReleaseBuf(data)

	return r.client.Set(ctx, key, data, expirationTime.Sub(time.Now())).Err()
}

// Close closes the redis client.
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// packRedisData packs storedTime, expirationTime and msg m into one byte slice.
// The returned []byte should be released by pool.ReleaseBuf().
func packRedisData(storedTime, expirationTime time.Time, m *dns.Msg) ([]byte, error) {
	wireMsg, mb, err := pool.PackBuffer(m)
	if err != nil {
		return nil, err
	}
	defer pool.ReleaseBuf(mb)

	v := pool.GetBuf(8 + 8 + len(wireMsg))
	binary.BigEndian.PutUint64(v[:8], uint64(storedTime.Unix()))
	binary.BigEndian.PutUint64(v[8:16], uint64(expirationTime.Unix()))
	copy(v[16:], wireMsg)
	return v, nil
}

func unpackRedisValue(b []byte) (storedTime, expirationTime time.Time, m *dns.Msg, err error) {
	if len(b) < 16 {
		return time.Time{}, time.Time{}, nil, errors.New("b is too short")
	}
	storedTime = time.Unix(int64(binary.BigEndian.Uint64(b[:8])), 0)
	expirationTime = time.Unix(int64(binary.BigEndian.Uint64(b[8:16])), 0)

	m = new(dns.Msg)
	err = m.Unpack(b[16:])
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	return storedTime, expirationTime, m, nil
}
