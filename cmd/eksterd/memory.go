/*
   Microsub server
   Copyright (C) 2018  Peter Stuifzand

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU General Public License for more details.

   You should have received a copy of the GNU General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/garyburd/redigo/redis"
	"github.com/pstuifzand/ekster/pkg/feedbin"
	"github.com/pstuifzand/ekster/pkg/microsub"
	"willnorris.com/go/microformats"
)

type memoryBackend struct {
	Channels      map[string]microsub.Channel
	Feeds         map[string][]microsub.Feed
	NextUid       int
	Me            string
	TokenEndpoint string

	ticker *time.Ticker
	quit   chan struct{}
}

type Debug interface {
	Debug()
}

func init() {
}

func (b *memoryBackend) Debug() {
	fmt.Println(b.Channels)
}

func (b *memoryBackend) load() error {
	filename := "backend.json"
	f, err := os.Open(filename)
	if err != nil {
		panic("cant open backend.json")
	}
	defer f.Close()
	jw := json.NewDecoder(f)
	err = jw.Decode(b)
	if err != nil {
		return err
	}

	conn := pool.Get()
	defer conn.Close()

	conn.Do("SETNX", "channel_sortorder_notifications", 1)

	conn.Do("DEL", "channels")

	for uid, channel := range b.Channels {
		log.Printf("loading channel %s - %s\n", uid, channel.Name)
		// for _, feed := range b.Feeds[uid] {
		//log.Printf("- loading feed %s\n", feed.URL)
		// resp, err := b.Fetch3(uid, feed.URL)
		// if err != nil {
		// 	log.Printf("Error while Fetch3 of %s: %v\n", feed.URL, err)
		// 	continue
		// }
		// defer resp.Body.Close()
		// b.ProcessContent(uid, feed.URL, resp.Header.Get("Content-Type"), resp.Body)
		// }

		conn.Do("SADD", "channels", uid)
		conn.Do("SETNX", "channel_sortorder_"+uid, 99999)
	}
	return nil
}

func (b *memoryBackend) save() {
	filename := "backend.json"
	f, _ := os.Create(filename)
	defer f.Close()
	jw := json.NewEncoder(f)
	jw.SetIndent("", "    ")
	jw.Encode(b)
}

func loadMemoryBackend() microsub.Microsub {
	backend := &memoryBackend{}
	err := backend.load()
	if err != nil {
		log.Printf("Error while loadingbackend: %v\n", err)
		return nil
	}

	return backend
}

func createMemoryBackend() microsub.Microsub {
	backend := memoryBackend{}
	defer backend.save()
	backend.Channels = make(map[string]microsub.Channel)
	backend.Feeds = make(map[string][]microsub.Feed)
	channels := []microsub.Channel{
		microsub.Channel{UID: "notifications", Name: "Notifications"},
		microsub.Channel{UID: "home", Name: "Home"},
	}
	for _, c := range channels {
		backend.Channels[c.UID] = c
	}
	backend.NextUid = 1000000

	backend.Me = "https://example.com/"

	log.Println(`Config file "backend.json" is created in the current directory.`)
	log.Println(`Update "Me" variable to your website address "https://example.com/"`)
	log.Println(`Update "TokenEndpoint" variable to the address of your token endpoint "https://example.com/token"`)
	return &backend
}

// ChannelsGetList gets channels
func (b *memoryBackend) ChannelsGetList() ([]microsub.Channel, error) {
	conn := pool.Get()
	defer conn.Close()

	channels := []microsub.Channel{}
	uids, err := redis.Strings(conn.Do("SORT", "channels", "BY", "channel_sortorder_*", "ASC"))
	if err != nil {
		log.Printf("Sorting channels failed: %v\n", err)
		for _, v := range b.Channels {
			channels = append(channels, v)
		}
	} else {
		for _, uid := range uids {
			if c, e := b.Channels[uid]; e {
				channels = append(channels, c)
			}
		}
	}
	return channels, nil
}

// ChannelsCreate creates a channels
func (b *memoryBackend) ChannelsCreate(name string) (microsub.Channel, error) {
	defer b.save()

	conn := pool.Get()
	defer conn.Close()

	uid := fmt.Sprintf("%04d", b.NextUid)
	channel := microsub.Channel{
		UID:  uid,
		Name: name,
	}

	b.Channels[channel.UID] = channel
	b.Feeds[channel.UID] = []microsub.Feed{}
	b.NextUid++

	conn.Do("SADD", "channels", uid)
	conn.Do("SETNX", "channel_sortorder_"+uid, 99999)

	return channel, nil
}

// ChannelsUpdate updates a channels
func (b *memoryBackend) ChannelsUpdate(uid, name string) (microsub.Channel, error) {
	defer b.save()
	if c, e := b.Channels[uid]; e {
		c.Name = name
		b.Channels[uid] = c
		return c, nil
	}
	return microsub.Channel{}, fmt.Errorf("Channel %s does not exist", uid)
}

// ChannelsDelete deletes a channel
func (b *memoryBackend) ChannelsDelete(uid string) error {
	defer b.save()

	conn := pool.Get()
	defer conn.Close()

	conn.Do("SREM", "channels", uid)
	conn.Do("DEL", "channel_sortorder_"+uid)

	delete(b.Channels, uid)
	delete(b.Feeds, uid)

	return nil
}

func mapToAuthor(result map[string]string) *microsub.Card {
	item := &microsub.Card{}
	item.Type = "card"
	if name, e := result["name"]; e {
		item.Name = name
	}
	if u, e := result["url"]; e {
		item.URL = u
	}
	if photo, e := result["photo"]; e {
		item.Photo = photo
	}
	if value, e := result["longitude"]; e {
		item.Longitude = value
	}
	if value, e := result["latitude"]; e {
		item.Latitude = value
	}
	if value, e := result["country-name"]; e {
		item.CountryName = value
	}
	if value, e := result["locality"]; e {
		item.Locality = value
	}
	return item
}

func mapToItem(result map[string]interface{}) microsub.Item {
	item := microsub.Item{}

	item.Type = "entry"

	if name, e := result["name"]; e {
		item.Name = name.(string)
	}

	if url, e := result["url"]; e {
		item.URL = url.(string)
	}

	if uid, e := result["uid"]; e {
		item.UID = uid.(string)
	}

	if author, e := result["author"]; e {
		item.Author = mapToAuthor(author.(map[string]string))
	}

	if checkin, e := result["checkin"]; e {
		item.Checkin = mapToAuthor(checkin.(map[string]string))
	}

	if content, e := result["content"]; e {
		itemContent := &microsub.Content{}
		set := false
		if c, ok := content.(map[string]interface{}); ok {
			if html, e2 := c["html"]; e2 {
				itemContent.HTML = html.(string)
				set = true
			}
			if text, e2 := c["value"]; e2 {
				itemContent.Text = text.(string)
				set = true
			}
		}
		if set {
			item.Content = itemContent
		}
	}

	// TODO: Check how to improve this

	if value, e := result["like-of"]; e {
		for _, v := range value.([]interface{}) {
			if u, ok := v.(string); ok {
				item.LikeOf = append(item.LikeOf, u)
			}
		}
	}

	if value, e := result["repost-of"]; e {
		if repost, ok := value.(string); ok {
			item.RepostOf = append(item.RepostOf, repost)
		} else if repost, ok := value.([]interface{}); ok {
			for _, v := range repost {
				if u, ok := v.(string); ok {
					item.RepostOf = append(item.RepostOf, u)
				}
			}
		}
	}

	if value, e := result["bookmark-of"]; e {
		for _, v := range value.([]interface{}) {
			if u, ok := v.(string); ok {
				item.BookmarkOf = append(item.BookmarkOf, u)
			}
		}
	}

	if value, e := result["in-reply-to"]; e {
		if replyTo, ok := value.(string); ok {
			item.InReplyTo = append(item.InReplyTo, replyTo)
		} else if valueArray, ok := value.([]interface{}); ok {
			for _, v := range valueArray {
				if replyTo, ok := v.(string); ok {
					item.InReplyTo = append(item.InReplyTo, replyTo)
				} else if cite, ok := v.(map[string]interface{}); ok {
					item.InReplyTo = append(item.InReplyTo, cite["url"].(string))
				}
			}
		}
	}

	if value, e := result["photo"]; e {
		for _, v := range value.([]interface{}) {
			item.Photo = append(item.Photo, v.(string))
		}
	}

	if value, e := result["category"]; e {
		if cats, ok := value.([]string); ok {
			for _, v := range cats {
				item.Category = append(item.Category, v)
			}
		} else if cats, ok := value.([]interface{}); ok {
			for _, v := range cats {
				if cat, ok := v.(string); ok {
					item.Category = append(item.Category, cat)
				} else if cat, ok := v.(map[string]interface{}); ok {
					item.Category = append(item.Category, cat["value"].(string))
				}
			}
		} else if cat, ok := value.(string); ok {
			item.Category = append(item.Category, cat)
		}
	}

	if published, e := result["published"]; e {
		item.Published = published.(string)
	} else {
		item.Published = time.Now().Format(time.RFC3339)
	}

	if updated, e := result["updated"]; e {
		item.Updated = updated.(string)
	}

	if id, e := result["_id"]; e {
		item.ID = id.(string)
	}
	if read, e := result["_is_read"]; e {
		item.Read = read.(bool)
	}

	return item
}

func (b *memoryBackend) run() {
	b.ticker = time.NewTicker(10 * time.Minute)
	b.quit = make(chan struct{})

	go func() {
		for {
			select {
			case <-b.ticker.C:
				for uid := range b.Channels {
					for _, feed := range b.Feeds[uid] {
						resp, err := b.Fetch3(uid, feed.URL)
						if err != nil {
							log.Printf("Error while Fetch3 of %s: %v\n", feed.URL, err)
							continue
						}
						defer resp.Body.Close()
						b.ProcessContent(uid, feed.URL, resp.Header.Get("Content-Type"), resp.Body)
					}
				}
			case <-b.quit:
				b.ticker.Stop()
				return
			}
		}
	}()
}

func (b *memoryBackend) TimelineGet(before, after, channel string) (microsub.Timeline, error) {
	conn := pool.Get()
	defer conn.Close()

	if channel == "feedbin" {
		fb := feedbin.New(os.Getenv("FEEDBIN_USER"), os.Getenv("FEEDBIN_PASS"))

		entries, err := fb.Entries()

		if err != nil {
			log.Fatal(err)
		}

		feeds := make(map[int64]feedbin.Feed)

		var items []microsub.Item

		for _, entry := range entries {
			var item microsub.Item

			var feed feedbin.Feed
			e := false
			if feed, e = feeds[entry.FeedID]; !e {
				feeds[entry.FeedID], _ = fb.Feed(entry.FeedID)
				feed = feeds[entry.FeedID]
			}

			item.Type = "entry"
			item.Name = entry.Title
			item.Content = &microsub.Content{HTML: entry.Content}
			item.URL = entry.URL
			item.Published = entry.Published.Format(time.RFC3339)
			item.Author = &microsub.Card{Type: "card", Name: feed.Title, URL: feed.SiteURL}

			items = append(items, item)
		}
		return microsub.Timeline{
			Paging: microsub.Pagination{},
			Items:  items,
		}, nil
	}

	log.Printf("TimelineGet %s\n", channel)
	feeds, err := b.FollowGetList(channel)
	if err != nil {
		return microsub.Timeline{}, err
	}
	log.Println(feeds)

	items := []microsub.Item{}

	zchannelKey := fmt.Sprintf("zchannel:%s:posts", channel)
	//channelKey := fmt.Sprintf("channel:%s:posts", channel)

	//itemJsons, err := redis.ByteSlices(conn.Do("SORT", channelKey, "BY", "*->Published", "GET", "*->Data", "ASC", "ALPHA"))
	// if err != nil {
	// 	log.Println(err)
	// 	return microsub.Timeline{
	// 		Paging: microsub.Pagination{},
	// 		Items:  items,
	// 	}
	// }

	afterScore := "-inf"
	if len(after) != 0 {
		afterScore = "(" + after
	}
	beforeScore := "+inf"
	if len(before) != 0 {
		beforeScore = "(" + before
	}

	itemJSONs := [][]byte{}

	itemScores, err := redis.Strings(
		conn.Do(
			"ZRANGEBYSCORE",
			zchannelKey,
			afterScore,
			beforeScore,
			"LIMIT",
			0,
			20,
			"WITHSCORES",
		),
	)

	if err != nil {
		return microsub.Timeline{
			Paging: microsub.Pagination{},
			Items:  items,
		}, err
	}

	if len(itemScores) >= 2 {
		before = itemScores[1]
		after = itemScores[len(itemScores)-1]
	}

	for i := 0; i < len(itemScores); i += 2 {
		itemID := itemScores[i]
		itemJSON, err := redis.Bytes(conn.Do("HGET", itemID, "Data"))
		if err != nil {
			log.Println(err)
			continue
		}
		itemJSONs = append(itemJSONs, itemJSON)
	}

	for _, obj := range itemJSONs {
		item := microsub.Item{}
		err := json.Unmarshal(obj, &item)
		if err != nil {
			// FIXME: what should we do if one of the items doen't unmarshal?
			log.Println(err)
			continue
		}
		item.Read = false
		items = append(items, item)
	}
	paging := microsub.Pagination{
		After:  after,
		Before: before,
	}

	return microsub.Timeline{
		Paging: paging,
		Items:  items,
	}, nil
}

//panic if s is not a slice
func reverseSlice(s interface{}) {
	size := reflect.ValueOf(s).Len()
	swap := reflect.Swapper(s)
	for i, j := 0, size-1; i < j; i, j = i+1, j-1 {
		swap(i, j)
	}
}

// func (b *memoryBackend) checkRead(channel string, uid string) bool {
// 	conn := pool.Get()
// 	defer conn.Close()
// 	args := redis.Args{}.Add(fmt.Sprintf("timeline:%s:read", channel)).Add("item:" + uid)
// 	member, err := redis.Bool(conn.Do("SISMEMBER", args...))
// 	if err != nil {
// 		log.Printf("Checking read for channel %s item %s has failed\n", channel, uid)
// 	}
// 	return member
// }

// func (b *memoryBackend) wasRead(channel string, item map[string]interface{}) bool {
// 	if uid, e := item["uid"]; e {
// 		uid = hex.EncodeToString([]byte(uid.(string)))
// 		return b.checkRead(channel, uid.(string))
// 	}

// 	if uid, e := item["url"]; e {
// 		uid = hex.EncodeToString([]byte(uid.(string)))
// 		return b.checkRead(channel, uid.(string))
// 	}

// 	return false
// }

func (b *memoryBackend) FollowGetList(uid string) ([]microsub.Feed, error) {
	return b.Feeds[uid], nil
}

func (b *memoryBackend) FollowURL(uid string, url string) (microsub.Feed, error) {
	defer b.save()
	feed := microsub.Feed{Type: "feed", URL: url}

	resp, err := b.Fetch3(uid, feed.URL)
	if err != nil {
		return feed, err
	}
	defer resp.Body.Close()

	b.Feeds[uid] = append(b.Feeds[uid], feed)

	b.ProcessContent(uid, feed.URL, resp.Header.Get("Content-Type"), resp.Body)

	return feed, nil
}

func (b *memoryBackend) UnfollowURL(uid string, url string) error {
	defer b.save()
	index := -1
	for i, f := range b.Feeds[uid] {
		if f.URL == url {
			index = i
			break
		}
	}
	if index >= 0 {
		feeds := b.Feeds[uid]
		b.Feeds[uid] = append(feeds[:index], feeds[index+1:]...)
	}

	return nil
}

func checkURL(u string) bool {
	testURL, err := url.Parse(u)
	if err != nil {
		return false
	}

	resp, err := http.Head(testURL.String())

	if err != nil {
		log.Printf("Error while HEAD %s: %v\n", u, err)
		return false
	}

	defer resp.Body.Close()

	return resp.StatusCode == 200
}

func getPossibleURLs(query string) []string {
	urls := []string{}
	if !(strings.HasPrefix(query, "https://") || strings.HasPrefix(query, "http://")) {
		secureURL := "https://" + query
		if checkURL(secureURL) {
			urls = append(urls, secureURL)
		} else {
			unsecureURL := "http://" + query
			if checkURL(unsecureURL) {
				urls = append(urls, unsecureURL)
			}
		}
	} else {
		urls = append(urls, query)
	}
	return urls
}

func (b *memoryBackend) Search(query string) ([]microsub.Feed, error) {
	urls := getPossibleURLs(query)

	feeds := []microsub.Feed{}

	for _, u := range urls {
		log.Println(u)
		resp, err := Fetch2(u)
		if err != nil {
			log.Printf("Error while fetching %s: %v\n", u, err)
			continue
		}
		fetchUrl, err := url.Parse(u)
		md := microformats.Parse(resp.Body, fetchUrl)
		if err != nil {
			log.Printf("Error while fetching %s: %v\n", u, err)
			continue
		}

		feedResp, err := Fetch2(fetchUrl.String())
		if err != nil {
			log.Printf("Error in fetch of %s - %v\n", fetchUrl, err)
			continue
		}
		defer feedResp.Body.Close()

		parsedFeed, err := b.feedHeader(fetchUrl.String(), feedResp.Header.Get("Content-Type"), feedResp.Body)
		if err != nil {
			log.Printf("Error in parse of %s - %v\n", fetchUrl, err)
			continue
		}

		feeds = append(feeds, parsedFeed)

		if alts, e := md.Rels["alternate"]; e {
			for _, alt := range alts {
				relURL := md.RelURLs[alt]
				log.Printf("alternate found with type %s %#v\n", relURL.Type, relURL)

				if strings.HasPrefix(relURL.Type, "text/html") || strings.HasPrefix(relURL.Type, "application/json") || strings.HasPrefix(relURL.Type, "application/xml") || strings.HasPrefix(relURL.Type, "text/xml") || strings.HasPrefix(relURL.Type, "application/rss+xml") || strings.HasPrefix(relURL.Type, "application/atom+xml") {
					feedResp, err := Fetch2(alt)
					if err != nil {
						log.Printf("Error in fetch of %s - %v\n", alt, err)
						continue
					}
					defer feedResp.Body.Close()

					parsedFeed, err := b.feedHeader(alt, feedResp.Header.Get("Content-Type"), feedResp.Body)
					if err != nil {
						log.Printf("Error in parse of %s - %v\n", alt, err)
						continue
					}

					feeds = append(feeds, parsedFeed)
				}
			}
		}
	}

	return feeds, nil
}

func (b *memoryBackend) PreviewURL(previewURL string) (microsub.Timeline, error) {
	resp, err := Fetch2(previewURL)
	if err != nil {
		return microsub.Timeline{}, fmt.Errorf("error while fetching %s: %v", previewURL, err)
	}
	items, err := b.feedItems(previewURL, resp.Header.Get("content-type"), resp.Body)
	if err != nil {
		return microsub.Timeline{}, fmt.Errorf("error while fetching %s: %v", previewURL, err)
	}

	return microsub.Timeline{
		Items: items,
	}, nil
}

func (b *memoryBackend) MarkRead(channel string, uids []string) error {
	conn := pool.Get()
	defer conn.Close()

	log.Printf("Marking read for %s %v\n", channel, uids)

	itemUIDs := []string{}
	for _, uid := range uids {
		itemUIDs = append(itemUIDs, "item:"+uid)
	}

	channelKey := fmt.Sprintf("channel:%s:read", channel)
	args := redis.Args{}.Add(channelKey).AddFlat(itemUIDs)

	if _, err := conn.Do("SADD", args...); err != nil {
		log.Printf("Marking read for channel %s has failed\n", channel)
		return err
	}

	zchannelKey := fmt.Sprintf("zchannel:%s:posts", channel)
	args = redis.Args{}.Add(zchannelKey).AddFlat(itemUIDs)

	if _, err := conn.Do("ZREM", args...); err != nil {
		log.Printf("Marking read for channel %s has failed\n", channel)
		return err
	}

	unread, _ := redis.Int(conn.Do("ZCARD", zchannelKey))
	unread -= len(uids)

	if ch, e := b.Channels[channel]; e {
		if unread < 0 {
			unread = 0
		}
		ch.Unread = unread
		b.Channels[channel] = ch
	}

	log.Printf("Marking read success for %s %v\n", channel, itemUIDs)

	return nil
}
