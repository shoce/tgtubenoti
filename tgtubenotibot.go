/*

https://console.cloud.google.com/apis/credentials
https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas


go mod init github.com/shoce/tgtubenotibot
go get -a -u -v
go mod tidy

GoFmt
GoBuildNull

*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	youtubeoption "google.golang.org/api/option"
	youtube "google.golang.org/api/youtube/v3"
	yaml "gopkg.in/yaml.v3"
)

const (
	NL = "\n"

	//YtEventType string = "completed"
	YtEventType string = "upcoming"

	YtMaxResults = 50
)

var (
	DEBUG bool

	YamlConfigPath string = "tgtubenotibot.yaml"

	KvToken       string
	KvAccountId   string
	KvNamespaceId string

	TgToken      string
	TgChatId     string
	TgBossChatId string

	// https://console.cloud.google.com/apis/credentials
	YtKey       string
	YtUsername  string
	YtChannelId string

	YtCheckInterval         string
	YtCheckIntervalDuration time.Duration
	YtCheckLast             string
	YtCheckLastTime         time.Time

	YtNextLive                string
	YtNextLiveTime            time.Time
	YtNextLivePublishedAt     string
	YtNextLivePublishedAtTime time.Time
	YtNextLiveId              string
	YtNextLiveTitle           string
	YtNextLiveReminderSent    string

	YtLastPublishedAt string

	TzMoscow   *time.Location
	HttpClient = &http.Client{}

	YtSvc        *youtube.Service
	YtPlaylistId string
)

func log(msg string, args ...interface{}) {
	t := time.Now().Local()
	ts := fmt.Sprintf(
		"%03d."+"%02d%02d."+"%02d"+"%02d.",
		t.Year()%1000, t.Month(), t.Day(), t.Hour(), t.Minute(),
	)
	msgtext := fmt.Sprintf("%s %s", ts, msg) + NL
	fmt.Fprintf(os.Stderr, msgtext, args...)
}

func tglog(msg string, args ...interface{}) error {
	log(msg, args...)
	msgtext := fmt.Sprintf(msg, args...) + NL

	type TgSendMessageRequest struct {
		ChatId              string `json:"chat_id"`
		Text                string `json:"text"`
		ParseMode           string `json:"parse_mode,omitempty"`
		DisableNotification bool   `json:"disable_notification"`
	}

	type TgSendMessageResponse struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageId int64 `json:"message_id"`
		} `json:"result"`
	}

	smreq := TgSendMessageRequest{
		ChatId:              TgBossChatId,
		Text:                msgtext,
		ParseMode:           "",
		DisableNotification: true,
	}
	smreqjs, err := json.Marshal(smreq)
	if err != nil {
		return fmt.Errorf("tglog json marshal: %w", err)
	}
	smreqjsBuffer := bytes.NewBuffer(smreqjs)

	var resp *http.Response
	tgapiurl := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TgToken)
	resp, err = http.Post(
		tgapiurl,
		"application/json",
		smreqjsBuffer,
	)
	if err != nil {
		return fmt.Errorf("tglog apiurl:`%s` apidata:`%s`: %w", tgapiurl, smreqjs, err)
	}

	var smresp TgSendMessageResponse
	err = json.NewDecoder(resp.Body).Decode(&smresp)
	if err != nil {
		return fmt.Errorf("tglog decode response: %w", err)
	}
	if !smresp.OK {
		return fmt.Errorf("tglog apiurl:`%s` apidata:`%s` api response not ok: %+v", tgapiurl, smreqjs, smresp)
	}

	return nil
}

func GetVar(name string) (value string, err error) {
	if DEBUG {
		//log("DEBUG GetVar: %s", name)
	}

	value = os.Getenv(name)
	if value != "" {
		return value, nil
	}

	if YamlConfigPath != "" {
		value, err = YamlGet(name)
		if err != nil {
			log("ERROR GetVar YamlGet %s: %v", name, err)
			return "", err
		}
		if value != "" {
			return value, nil
		}
	}

	if KvToken != "" && KvAccountId != "" && KvNamespaceId != "" {
		if v, err := KvGet(name); err != nil {
			log("ERROR GetVar KvGet %s: %v", name, err)
			return "", err
		} else {
			value = v
		}
	}

	return value, nil
}

func SetVar(name, value string) (err error) {
	if DEBUG {
		log("DEBUG SetVar: %s: %s", name, value)
	}

	if KvToken != "" && KvAccountId != "" && KvNamespaceId != "" {
		err = KvSet(name, value)
		if err != nil {
			return err
		}
		return nil
	}

	if YamlConfigPath != "" {
		err = YamlSet(name, value)
		if err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("not kv credentials nor yaml config path provided to save to")
}

func YamlGet(name string) (value string, err error) {
	configf, err := os.Open(YamlConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer configf.Close()

	configm := make(map[interface{}]interface{})
	if err = yaml.NewDecoder(configf).Decode(&configm); err != nil {
		if DEBUG {
			log("WARNING yaml.Decode %s: %v", YamlConfigPath, err)
		}
		return "", err
	}

	if v, ok := configm[name]; ok == true {
		switch v.(type) {
		case string:
			value = v.(string)
		case int:
			value = fmt.Sprintf("%d", v.(int))
		default:
			return "", fmt.Errorf("yaml value of unsupported type, only string and int types are supported")
		}
	}

	return value, nil
}

func YamlSet(name, value string) error {
	configf, err := os.Open(YamlConfigPath)
	if err == nil {
		configm := make(map[interface{}]interface{})
		err := yaml.NewDecoder(configf).Decode(&configm)
		if err != nil {
			log("WARNING yaml.Decode %s: %v", YamlConfigPath, err)
		}
		configf.Close()
		configm[name] = value
		configf, err := os.Create(YamlConfigPath)
		if err == nil {
			defer configf.Close()
			confige := yaml.NewEncoder(configf)
			err := confige.Encode(configm)
			if err == nil {
				confige.Close()
				configf.Close()
			} else {
				log("WARNING yaml.Encoder.Encode: %v", err)
				return err
			}
		} else {
			log("WARNING os.Create config file %s: %v", YamlConfigPath, err)
			return err
		}
	} else {
		log("WARNING os.Open config file %s: %v", YamlConfigPath, err)
		return err
	}

	return nil
}

func KvGet(name string) (value string, err error) {
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", KvAccountId, KvNamespaceId, name),
		nil,
	)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", KvToken))
	resp, err := HttpClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("kv api response status: %s", resp.Status)
	}

	if rbb, err := io.ReadAll(resp.Body); err != nil {
		return "", err
	} else {
		value = string(rbb)
	}

	return value, nil
}

func KvSet(name, value string) error {
	mpbb := new(bytes.Buffer)
	mpw := multipart.NewWriter(mpbb)
	if err := mpw.WriteField("metadata", "{}"); err != nil {
		return err
	}
	if err := mpw.WriteField("value", value); err != nil {
		return err
	}
	mpw.Close()

	req, err := http.NewRequest(
		"PUT",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/storage/kv/namespaces/%s/values/%s", KvAccountId, KvNamespaceId, name),
		mpbb,
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", mpw.FormDataContentType())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", KvToken))
	resp, err := HttpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("kv api response status: %s", resp.Status)
	}

	return nil
}

func init() {
	var err error

	TzMoscow, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		tglog("ERROR time.LoadLocation %s", err)
		os.Exit(1)
	}

	if os.Getenv("YamlConfigPath") != "" {
		YamlConfigPath = os.Getenv("YamlConfigPath")
	}
	if YamlConfigPath == "" {
		log("WARNING YamlConfigPath empty")
	}

	KvToken, err = GetVar("KvToken")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if KvToken == "" {
		log("WARNING KvToken empty")
	}

	KvAccountId, err = GetVar("KvAccountId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if KvAccountId == "" {
		log("WARNING KvAccountId empty")
	}

	KvNamespaceId, err = GetVar("KvNamespaceId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if KvNamespaceId == "" {
		log("WARNING KvNamespaceId empty")
	}

	VarDEBUG, err := GetVar("DEBUG")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if VarDEBUG != "" {
		DEBUG = true
	}

	TgToken, err = GetVar("TgToken")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	TgChatId, err = GetVar("TgChatId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if TgChatId == "" {
		log("ERROR TgChatId empty")
		os.Exit(1)
	}

	TgBossChatId, err = GetVar("TgBossChatId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if TgBossChatId == "" {
		log("ERROR TgBossChatId empty")
		os.Exit(1)
	}

	YtKey, err = GetVar("YtKey")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if YtKey == "" {
		log("ERROR: YtKey empty")
		os.Exit(1)
	}

	YtUsername, err = GetVar("YtUsername")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	YtChannelId, err = GetVar("YtChannelId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	if YtUsername == "" && YtChannelId == "" {
		tglog("YtUsername and YtChannelId empty")
		os.Exit(1)
	}

	YtCheckInterval, err = GetVar("YtCheckInterval")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if YtCheckInterval == "" {
		log("ERROR YtCheckInterval empty")
		os.Exit(1)
	}
	YtCheckIntervalDuration, err = time.ParseDuration(YtCheckInterval)
	if err != nil {
		log("ERROR YtCheckInterval %s", err)
		os.Exit(1)
	}

	YtCheckLast, err = GetVar("YtCheckLast")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if YtCheckLast != "" {
		YtCheckLastTime, err = time.Parse(time.RFC3339, YtCheckLast)
		log("YtCheckLastTime: %v", YtCheckLastTime)
		if err != nil {
			log("WARNING YtCheckLast %s", err)
			log("WARNING YtCheckLast setting to empty")
			err = SetVar("YtCheckLast", "")
			if err != nil {
				tglog("WARNING SetVar YtCheckLast: %s", err)
			}
		}
	}

	YtNextLive, err = GetVar("YtNextLive")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if YtNextLive != "" {
		YtNextLiveTime, err = time.Parse(time.RFC3339, YtNextLive)
		if err != nil {
			log("WARNING YtNextLive Parse: %s", err)
			log("WARNING YtNextLive setting to empty")
			err = SetVar("YtNextLive", "")
			if err != nil {
				tglog("WARNING SetVar YtNextLive: %s", err)
			}
		}
	}

	YtNextLivePublishedAt, err = GetVar("YtNextLivePublishedAt")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}
	if YtNextLivePublishedAt != "" {
		YtNextLivePublishedAtTime, err = time.Parse(time.RFC3339, YtNextLivePublishedAt)
		if err != nil {
			log("WARNING YtNextLivePublishedAt %s", err)
			log("WARNING YtNextLivePublishedAt setting to empty")
			err = SetVar("YtNextLivePublishedAt", "")
			if err != nil {
				tglog("WARNING SetVar YtNextLivePublishedAt: %s", err)
			}
		}
	}

	YtNextLiveId, err = GetVar("YtNextLiveId")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	YtNextLiveTitle, err = GetVar("YtNextLiveTitle")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	YtNextLiveReminderSent, err = GetVar("YtNextLiveReminderSent")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

	YtLastPublishedAt, err = GetVar("YtLastPublishedAt")
	if err != nil {
		log("ERROR %s", err)
		os.Exit(1)
	}

}

func monthnameru(m time.Month) string {
	switch m {
	case time.January:
		return "январь"
	case time.February:
		return "февраль"
	case time.March:
		return "март"
	case time.April:
		return "апрель"
	case time.May:
		return "май"
	case time.June:
		return "июнь"
	case time.July:
		return "июль"
	case time.August:
		return "август"
	case time.September:
		return "сентябрь"
	case time.October:
		return "октябрь"
	case time.November:
		return "ноябрь"
	case time.December:
		return "декабрь"
	}
	return "янвабрь"
}

func httpPostJson(url string, data *bytes.Buffer, target interface{}) error {
	resp, err := HttpClient.Post(
		url,
		"application/json",
		data,
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody := bytes.NewBuffer(nil)
	_, err = io.Copy(respBody, resp.Body)
	if err != nil {
		return fmt.Errorf("io.Copy: %v", err)
	}

	err = json.NewDecoder(respBody).Decode(target)
	if err != nil {
		return fmt.Errorf("json.Decode: %v", err)
	}

	return nil
}

type TgPhotoSize struct {
	FileId       string `json:"file_id"`
	FileUniqueId string `json:"file_unique_id"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
	FileSize     int64  `json:"file_size"`
}

type TgMessage struct {
	Id        string
	MessageId int64         `json:"message_id"`
	Photo     []TgPhotoSize `json:"photo"`
}

type TgResponse struct {
	Ok          bool       `json:"ok"`
	Description string     `json:"description"`
	Result      *TgMessage `json:"result"`
}

func tgEscapeMarkdown(text string) string {
	return strings.NewReplacer(
		"(", "\\(",
		")", "\\)",
		"[", "\\[",
		"]", "\\]",
		"{", "\\{",
		"}", "\\}",
		"~", "\\~",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"!", "\\!",
		".", "\\.",
	).Replace(text)
}

func tgEscape(s string) string {
	return strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"~", "\\~",
		"`", "\\`",
	).Replace(s)
}

func tgSendPhoto(chatid, photourl, caption string) (msg *TgMessage, err error) {
	// https://core.telegram.org/bots/api#sendphoto

	caption = tgEscapeMarkdown(caption)

	sendphoto := map[string]interface{}{
		"chat_id":    chatid,
		"photo":      photourl,
		"caption":    caption,
		"parse_mode": "MarkdownV2",
	}
	sendphotojson, err := json.Marshal(sendphoto)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = httpPostJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", TgToken),
		bytes.NewBuffer(sendphotojson),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("tgSendPhoto: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func tgSendMessage(chatid, text string) (msg *TgMessage, err error) {
	// https://core.telegram.org/bots/api#sendmessage

	text = tgEscapeMarkdown(text)

	sendmessage := map[string]interface{}{
		"chat_id":    chatid,
		"text":       text,
		"parse_mode": "MarkdownV2",

		"disable_web_page_preview": true,
	}
	sendmessagejson, err := json.Marshal(sendmessage)
	if err != nil {
		return nil, err
	}

	var tgresp TgResponse
	err = httpPostJson(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", TgToken),
		bytes.NewBuffer(sendmessagejson),
		&tgresp,
	)
	if err != nil {
		return nil, err
	}

	if !tgresp.Ok {
		return nil, fmt.Errorf("tgSendMessage: %s", tgresp.Description)
	}

	msg = tgresp.Result
	msg.Id = fmt.Sprintf("%d", msg.MessageId)

	return msg, nil
}

func ytgetplaylistid(ytusername string, ytchannelid string) (playlistid string, err error) {
	// https://pkg.go.dev/google.golang.org/api/youtube/v3#ChannelsListCall

	channelslistcall := YtSvc.Channels.List([]string{"id", "snippet", "contentDetails"}).MaxResults(6)
	if ytchannelid != "" {
		channelslistcall = channelslistcall.Id(ytchannelid)
	} else if ytusername != "" {
		channelslistcall = channelslistcall.ForUsername(ytusername)
	}
	channelslist, err := channelslistcall.Do()
	if err != nil {
		return "", fmt.Errorf("youtube channels list: %w", err)
	}

	if len(channelslist.Items) == 0 {
		return "", fmt.Errorf("youtube channels list: empty result")
	}
	if DEBUG {
		for _, c := range channelslist.Items {
			tglog(
				"channel title: %s / "+"id: %s / "+"playlist id: %s / ",
				c.Snippet.Title, c.Id, c.ContentDetails.RelatedPlaylists.Uploads,
			)
		}
	}
	if len(channelslist.Items) > 1 {
		return "", fmt.Errorf("channels list: more than one result")
	}

	playlistid = channelslist.Items[0].ContentDetails.RelatedPlaylists.Uploads

	return playlistid, nil
}

func ytplaylistitemslist(ytplaylistid string, publishedafter string) (ytvideosids []string, err error) {
	// https://developers.google.com/youtube/v3/docs/playlistItems/list
	// https://pkg.go.dev/google.golang.org/api/youtube/v3#PlaylistItemsListCall
	// https://pkg.go.dev/google.golang.org/api/youtube/v3#PlaylistItemSnippet
	// https://pkg.go.dev/google.golang.org/api/youtube/v3#PlaylistItem

	playlistitemslistcall := YtSvc.PlaylistItems.List([]string{"snippet", "contentDetails"}).MaxResults(YtMaxResults)
	playlistitemslistcall = playlistitemslistcall.PlaylistId(ytplaylistid)
	err = playlistitemslistcall.Pages(
		context.TODO(),
		func(r *youtube.PlaylistItemListResponse) error {
			for _, i := range r.Items {
				if i.Snippet.PublishedAt > publishedafter {
					ytvideosids = append(ytvideosids, (*i).Snippet.ResourceId.VideoId)
				}
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("youtube playlistitems list: %v", err)
	}

	return ytvideosids, nil
}

func ytsearchlives() (ytvideosids []string, err error) {
	// https://developers.google.com/youtube/v3/docs/search/list

	// https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas
	// one search call costs 100 quota

	searchcall := YtSvc.Search.List([]string{"id", "snippet"}).MaxResults(6).Order("date").Type("video")
	searchcall = searchcall.ChannelId(YtChannelId).EventType(YtEventType)
	searchcall = searchcall.PublishedAfter(YtNextLivePublishedAtTime.Add(time.Second).Format(time.RFC3339))
	rs, err := searchcall.Do()
	if err != nil {
		return nil, fmt.Errorf("search lives: %w", err)
	}

	if DEBUG {
		log("DEBUG search.list %s lives response: %+v", YtEventType, rs)
		tglog("DEBUG search.list: %d items: ", len(rs.Items))
		for i, item := range rs.Items {
			tglog("DEBUG %03d/%03d id:%s title:`%s`", i+1, len(rs.Items), item.Id.VideoId, item.Snippet.Title)
		}
	}

	for _, i := range rs.Items {
		ytvideosids = append(ytvideosids, i.Id.VideoId)
	}

	return ytvideosids, nil
}

func ytvideoslist(ytvideosids []string) (ytvideos []youtube.Video, err error) {
	// https://developers.google.com/youtube/v3/docs/videos/list
	// https://pkg.go.dev/google.golang.org/api/youtube/v3#VideoListResponse
	// https://pkg.go.dev/google.golang.org/api/youtube/v3#Video

	v := YtSvc.Videos.List([]string{"snippet", "liveStreamingDetails"})
	v = v.Id(ytvideosids...)
	rv, err := v.Do()
	if err != nil {
		return nil, fmt.Errorf("videos list: %w", err)
	}
	if DEBUG {
		//log("DEBUG videos.list response: %+v", rv)
	}

	for _, v := range rv.Items {
		ytvideos = append(ytvideos, *v)
	}

	sort.Slice(ytvideos, func(i, j int) bool { return ytvideos[i].Snippet.PublishedAt < ytvideos[j].Snippet.PublishedAt })

	return ytvideos, nil
}

func ytvideoPhotoUrl(ytthumbs youtube.ThumbnailDetails) (photourl string) {
	switch {
	case ytthumbs.Maxres != nil && ytthumbs.Maxres.Url != "":
		photourl = ytthumbs.Maxres.Url
	case ytthumbs.Standard != nil && ytthumbs.Standard.Url != "":
		photourl = ytthumbs.Standard.Url
	case ytthumbs.High != nil && ytthumbs.High.Url != "":
		photourl = ytthumbs.High.Url
	case ytthumbs.Medium != nil && ytthumbs.Medium.Url != "":
		photourl = ytthumbs.Medium.Url
	case ytthumbs.Default != nil && ytthumbs.Default.Url != "":
		photourl = ytthumbs.Default.Url
	}
	return photourl
}

func tgpostpublished(ytvideo youtube.Video) error {
	var err error

	var photourl string
	if ytvideo.Snippet.Thumbnails != nil {
		photourl = ytvideoPhotoUrl(*ytvideo.Snippet.Thumbnails)
	}

	if DEBUG {
		log("photourl: %s"+NL, photourl)
	}

	caption := fmt.Sprintf(
		"Новое видео "+NL+
			"*%s* "+NL+
			"https://youtu.be/%s "+NL,
		tgEscape(ytvideo.Snippet.Title),
		tgEscape(ytvideo.Id),
	)

	if DEBUG {
		log("tgpostpublished photo caption: "+NL+"%s"+NL, caption)
	}

	msg, err := tgSendPhoto(TgChatId, photourl, caption)
	if err != nil {
		return fmt.Errorf("telegram send photo: %w", err)
	}

	log("posted telegram photo message id:%s"+NL, msg.Id)

	return nil
}

func tgpostnextlive(ytvideo youtube.Video) error {
	var err error

	if YtNextLive != "" {
		YtNextLiveTime, err = time.Parse(time.RFC3339, YtNextLive)
		if err != nil {
			return fmt.Errorf("time.Parse YtNextLive: %w", err)
		}
	}

	var photourl string
	if ytvideo.Snippet.Thumbnails != nil {
		photourl = ytvideoPhotoUrl(*ytvideo.Snippet.Thumbnails)
	}

	if DEBUG {
		log("tgpostnextlive photourl: %s"+NL, photourl)
	}

	caption := fmt.Sprintf(
		"Запланированный эфир "+NL+
			"*%s* "+NL+
			"*%s/%d %s* (московское время) "+NL+
			"https://youtu.be/%s "+NL,
		tgEscape(YtNextLiveTitle),
		strings.ToTitle(monthnameru(YtNextLiveTime.In(TzMoscow).Month())),
		YtNextLiveTime.In(TzMoscow).Day(),
		YtNextLiveTime.In(TzMoscow).Format("15:04"),
		tgEscape(YtNextLiveId),
	)

	if DEBUG {
		log("tgpostnextlive photo caption: "+NL+"%s"+NL, caption)
	}

	msg, err := tgSendPhoto(TgChatId, photourl, caption)
	if err != nil {
		return fmt.Errorf("telegram send photo: %w", err)
	}

	log("posted telegram photo message id:%s"+NL, msg.Id)

	return nil
}

func tgpostlivereminder() error {
	var err error

	text := fmt.Sprintf(
		"Через час начало эфира "+NL+
			"*%s* "+NL+
			"https://youtu.be/%s "+NL,
		tgEscape(YtNextLiveTitle),
		tgEscape(YtNextLiveId),
	)

	if DEBUG {
		log("tgpostlivereminder text: "+NL+"%s"+NL, text)
	}

	msg, err := tgSendMessage(TgChatId, text)
	if err != nil {
		return fmt.Errorf("telegram send message: %w", err)
	}

	log("posted telegram text message id:%s"+NL, msg.Id)

	return nil
}

func main() {
	var err error

	if YtNextLiveReminderSent != "true" && time.Now().Before(YtNextLiveTime) {
		tglog("next live %s `%s` in %s", YtNextLiveId, YtNextLiveTitle, YtNextLiveTime.Sub(time.Now()).Truncate(time.Minute))
	}

	if tonextlive := YtNextLiveTime.Sub(time.Now()); tonextlive > 58*time.Minute && tonextlive < 62*time.Minute {
		if YtNextLiveReminderSent != "true" {
			err = tgpostlivereminder()
			if err != nil {
				tglog("WARNING telegram post next live reminder: %s", err)
			} else {
				YtNextLiveReminderSent = "true"
				err = SetVar("YtNextLiveReminderSent", YtNextLiveReminderSent)
				if err != nil {
					tglog("WARNING SetVar YtNextLiveReminderSent: %s", err)
				}
			}
		}
	}

	// wait for YtCheckIntervalDuration

	if time.Now().Sub(YtCheckLastTime) < YtCheckIntervalDuration {
		if DEBUG {
			log("next youtube check in %v", YtCheckLastTime.Add(YtCheckIntervalDuration).Sub(time.Now()).Truncate(time.Second))
		}
		os.Exit(0)
	}

	// update YtCheckLastTime

	YtCheckLastTime = time.Now()

	YtCheckLast = YtCheckLastTime.UTC().Format(time.RFC3339)
	err = SetVar("YtCheckLast", YtCheckLast)
	if err != nil {
		tglog("ERROR SetVar YtCheckLast: %s", err)
		os.Exit(1)
	}

	// youtube service

	YtSvc, err = youtube.NewService(context.TODO(), youtubeoption.WithAPIKey(YtKey))
	if err != nil {
		tglog("ERROR youtube.NewService: %w", err)
		os.Exit(1)
	}

	YtPlaylistId, err = ytgetplaylistid(YtUsername, YtChannelId)
	if err != nil {
		tglog("ERROR get youtube playlist id: %w", err)
		os.Exit(1)
	}
	if YtPlaylistId == "" {
		tglog("ERROR YtPlaylistId empty")
		os.Exit(1)
	}

	// videos published in recent ten hours

	// https://pkg.go.dev/google.golang.org/api/youtube/v3#PlaylistItemSnippet
	var ytvideosids1 []string
	ytvideosids1, err = ytplaylistitemslist(YtPlaylistId, time.Now().Add(-10*time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		tglog("WARNING youtube list published in recent ten hours: %s", err)
	}

	var ytvideos1 []youtube.Video
	if len(ytvideosids1) > 0 {
		ytvideos1, err = ytvideoslist(ytvideosids1)
		if err != nil {
			tglog("WARNING youtube list published in recent ten hours: %s", err)
		}
	}

	if DEBUG {
		tglog("DEBUG published videos in recent ten hours : %d items: ", len(ytvideos1))
		for i, v := range ytvideos1 {
			tglog(
				"DEBUG %03d/%03d %s id:%s "+
					"PublishedAt:%s ScheduledStartTime:%s "+
					"ActualStartTime:%s ActualEndTime:%s",
				i+1, len(ytvideos1), v.Snippet.Title, v.Id,
				v.Snippet.PublishedAt, v.LiveStreamingDetails.ScheduledStartTime,
				v.LiveStreamingDetails.ActualStartTime, v.LiveStreamingDetails.ActualEndTime,
			)
		}
	}

	// videos published

	// https://pkg.go.dev/google.golang.org/api/youtube/v3#PlaylistItemSnippet
	var ytvideosids []string
	ytvideosids, err = ytplaylistitemslist(YtPlaylistId, YtLastPublishedAt)
	if err != nil {
		tglog("WARNING youtube list published: %s", err)
	}

	var ytvideos []youtube.Video
	if len(ytvideosids) > 0 {
		ytvideos, err = ytvideoslist(ytvideosids)
		if err != nil {
			tglog("WARNING youtube list published: %s", err)
		}
	}

	if DEBUG {
		tglog("DEBUG published videos: %d items ", len(ytvideos))
		for i, v := range ytvideos {
			tglog("DEBUG %03d/%03d id:%s title:`%s`", i+1, len(ytvideos), v.Id, v.Snippet.Title)
		}
	}

	for _, v := range ytvideos {
		err = tgpostpublished(v)
		if err != nil {
			tglog("ERROR telegram post published youtube video: %s", err)
			os.Exit(1)
		}
		YtLastPublishedAt = v.Snippet.PublishedAt
		err = SetVar("YtLastPublishedAt", YtLastPublishedAt)
		if err != nil {
			tglog("WARNING SetVar YtLastPublishedAt: %s", err)
		}
	}

	// lives

	var ytlivesids []string
	ytlivesids, err = ytsearchlives()
	if err != nil {
		tglog("ERROR youtube search lives: %s", err)
		os.Exit(1)
	}

	if len(ytlivesids) == 0 {
		os.Exit(0)
	}

	var ytlives []youtube.Video
	ytlives, err = ytvideoslist(ytvideosids)
	if err != nil {
		tglog("WARNING youtube list lives: %s", err)
	}

	if DEBUG {
		tglog("DEBUG lives: %d items: ", len(ytlives))
		for i, v := range ytlives {
			tglog("DEBUG %03d/%03d id:%s title:`%s`", i+1, len(ytlives), v.Id, v.Snippet.Title)
		}
	}

	nextlivevideo := ytlives[0]

	YtNextLive = nextlivevideo.LiveStreamingDetails.ScheduledStartTime
	err = SetVar("YtNextLive", YtNextLive)
	if err != nil {
		tglog("ERROR SetVar YtNextLive: %s", err)
		os.Exit(1)
	}

	YtNextLivePublishedAt = nextlivevideo.Snippet.PublishedAt
	err = SetVar("YtNextLivePublishedAt", YtNextLivePublishedAt)
	if err != nil {
		tglog("ERROR SetVar YtNextLivePublishedAt: %s", err)
		os.Exit(1)
	}

	YtNextLiveId = nextlivevideo.Id
	err = SetVar("YtNextLiveId", YtNextLiveId)
	if err != nil {
		tglog("ERROR SetVar YtNextLiveId: %s", err)
		os.Exit(1)
	}

	YtNextLiveTitle = nextlivevideo.Snippet.Title
	err = SetVar("YtNextLiveTitle", YtNextLiveTitle)
	if err != nil {
		tglog("ERROR SetVar YtNextLiveTitle: %s", err)
		os.Exit(1)
	}

	YtNextLiveReminderSent = ""
	err = SetVar("YtNextLiveReminderSent", YtNextLiveReminderSent)
	if err != nil {
		tglog("ERROR SetVar YtNextLiveReminderSent: %s", err)
		os.Exit(1)
	}

	err = tgpostnextlive(nextlivevideo)
	if err != nil {
		tglog("telegram post next live: %s", err)
		os.Exit(1)
	}

}
