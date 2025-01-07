/*

https://console.cloud.google.com/apis/credentials
https://console.cloud.google.com/apis/api/youtube.googleapis.com/quotas

        # every search requests costs 100 quota
        # total quota limit is 10'000 per day
        # twice an hour schedule uses 4800 quota per day
        # trice an hour schedule uses 7200 quota per day


GoGet
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
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	youtubeoption "google.golang.org/api/option"
	youtube "google.golang.org/api/youtube/v3"
	yaml "gopkg.in/yaml.v3"
)

const (
	NL   = "\n"
	SPAC = "    "

	//YtEventType string = "completed"
	YtEventType string = "upcoming"

	YtMaxResults = 50
)

type TgTubeNotiConfig struct {
	YssUrl string `yaml:"-"`

	DEBUG bool `yaml:"DEBUG"`

	Interval time.Duration `yaml:"Interval"`

	TgToken      string `yaml:"TgToken"`
	TgChatId     string `yaml:"TgChatId"`
	TgBossChatId string `yaml:"TgBossChatId"`

	// https://console.cloud.google.com/apis/credentials
	YtKey        string `yaml:"YtKey"`
	YtUsername   string `yaml:"YtUsername"`
	YtChannelId  string `yaml:"YtChannelId"`
	YtPlaylistId string `yaml:"YtPlaylistId"`

	YtCheckInterval time.Duration `yaml:"YtCheckInterval"`
	YtCheckLast     time.Time     `yaml:"YtCheckLast"`

	YtNextLive             time.Time `yaml:"YtNextLive"`
	YtNextLiveId           string    `yaml:"YtNextLiveId"`
	YtNextLiveTitle        string    `yaml:"YtNextLiveTitle"`
	YtNextLiveReminderSent bool      `yaml:"YtNextLiveReminderSent"`

	YtLastPublishedAt string `yaml:"YtLastPublishedAt"`

	TgTimezoneName      string `yaml:"TgTimezoneName"`
	TgTimezoneNameShort string `yaml:"TgTimezoneNameShort"`
	TgLang              string `yaml:"TgLang"`
}

var (
	Config TgTubeNotiConfig

	HttpClient = &http.Client{}

	YtSvc *youtube.Service

	TgTimezone *time.Location

	TgLangMessages = map[string]map[string]string{
		"deutsch": map[string]string{
			"published":    "Neues Video",
			"nextlive":     "Bevorstehender Livestream",
			"livereminder": "Der Livestream beginnt in einer Stunde",
		},
		"english": map[string]string{
			"published":    "New video",
			"nextlive":     "Upcoming live",
			"livereminder": "Live starts in one hour",
		},
		"french": map[string]string{
			"published":    "Nouveau vidéo",
			"nextlive":     "Prochain live",
			"livereminder": "Le live commence dans une heure",
		},
		"hindi": map[string]string{
			"published":    "नया वीडियो",
			"nextlive":     "आगामी लाइव",
			"livereminder": "लाइव एक घंटे में शुरू होगा",
		},
		"russian": map[string]string{
			"published":    "Новое видео",
			"nextlive":     "Запланированный эфир",
			"livereminder": "Через час начало эфира",
		},
		"spanish": map[string]string{
			"published":    "Nuevo video",
			"nextlive":     "Próximo en vivo",
			"livereminder": "El directo comienza en una hora",
		},
		"ukrainian": map[string]string{
			"published":    "Нове відео",
			"nextlive":     "Запланований ефір",
			"livereminder": "Через годину початок ефіру",
		},
	}
)

func init() {
	var err error

	if v := os.Getenv("YssUrl"); v != "" {
		Config.YssUrl = v
	}
	if Config.YssUrl == "" {
		log("ERROR YssUrl empty")
		os.Exit(1)
	}

	if err := Config.Get(); err != nil {
		log("ERROR Config.Get: %v", err)
		os.Exit(1)
	}

	log("Interval: %v", Config.Interval)
	if Config.Interval == 0 {
		log("ERROR Interval empty")
		os.Exit(1)
	}

	if Config.TgToken == "" {
		log("ERROR TgToken empty")
		os.Exit(1)
	}

	if Config.TgLang == "" {
		log("ERROR TgLang empty")
		os.Exit(1)
	}
	if _, ok := TgLangMessages[Config.TgLang]; !ok {
		log("ERROR TgLang `%s` not supported")
		os.Exit(1)
	}
	log("DEBUG TgLang: %s", Config.TgLang)

	if Config.TgTimezoneName == "" {
		log("ERROR TgTimezoneName empty")
		os.Exit(1)
	}
	TgTimezone, err = time.LoadLocation(Config.TgTimezoneName)
	if err != nil {
		tglog("ERROR time.LoadLocation `%s`: %w", Config.TgTimezoneName, err)
		os.Exit(1)
	}
	log("DEBUG TgTimezoneName: %s", Config.TgTimezoneName)

	Config.TgTimezoneNameShort = Config.TgTimezoneName
	Config.TgTimezoneNameShort = strings.ToLower(Config.TgTimezoneNameShort)
	Config.TgTimezoneNameShort = strings.ReplaceAll(Config.TgTimezoneNameShort, "_", " ")
	if i := strings.LastIndex(Config.TgTimezoneNameShort, "/"); i >= 0 && len(Config.TgTimezoneNameShort) > i+1 {
		Config.TgTimezoneNameShort = Config.TgTimezoneNameShort[i+1:]
	}
	log("DEBUG TgTimezoneNameShort: %s", Config.TgTimezoneNameShort)

	if Config.TgChatId == "" {
		log("ERROR TgChatId empty")
		os.Exit(1)
	}

	if Config.TgBossChatId == "" {
		log("ERROR TgBossChatId empty")
		os.Exit(1)
	}

	if Config.YtKey == "" {
		log("ERROR: YtKey empty")
		os.Exit(1)
	}

	if Config.YtUsername == "" && Config.YtChannelId == "" {
		tglog("YtUsername and YtChannelId empty")
		os.Exit(1)
	}

	if Config.YtCheckInterval == 0 {
		log("ERROR YtCheckInterval empty")
		os.Exit(1)
	}
}

func main() {
	var err error

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	go func(sigterm chan os.Signal) {
		<-sigterm
		tglog("%s: sigterm", os.Args[0])
		log("sigterm received")
		os.Exit(1)
	}(sigterm)

	for {
		t0 := time.Now()

		err = CheckTube()
		if err != nil {
			log("WARNING CheckTube: %v", err)
		}

		if dur := time.Now().Sub(t0); dur < Config.Interval {
			time.Sleep(Config.Interval - dur)
		}
	}

}

func CheckTube() (err error) {
	if Config.DEBUG {
		if !Config.YtNextLiveReminderSent && time.Now().Before(Config.YtNextLive) {
			log("DEBUG next live %s `%s` in %s", Config.YtNextLiveId, Config.YtNextLiveTitle, Config.YtNextLive.Sub(time.Now()).Truncate(time.Minute))
		}
	}

	if tonextlive := Config.YtNextLive.Sub(time.Now()); tonextlive > 57*time.Minute && tonextlive < 61*time.Minute {
		if !Config.YtNextLiveReminderSent {
			err = tgpostlivereminder()
			if err != nil {
				tglog("WARNING telegram post next live reminder: %s", err)
			} else {
				Config.YtNextLiveReminderSent = true
				err = Config.Put()
				if err != nil {
					log("ERROR Config.Put: %s", err)
				}
			}
		}
	}

	// wait for YtCheckIntervalDuration

	if time.Now().Sub(Config.YtCheckLast) < Config.YtCheckInterval {
		if Config.DEBUG {
			log("DEBUG next youtube check in %v", Config.YtCheckLast.Add(Config.YtCheckInterval).Sub(time.Now()).Truncate(time.Second))
		}
		return nil
	}

	// update YtCheckLastTime

	Config.YtCheckLast = time.Now().UTC().Truncate(time.Second)

	err = Config.Put()
	if err != nil {
		log("ERROR Config.Put: %s", err)
	}

	// youtube service

	YtSvc, err = youtube.NewService(context.TODO(), youtubeoption.WithAPIKey(Config.YtKey))
	if err != nil {
		tglog("ERROR youtube.NewService: %w", err)
		return fmt.Errorf("youtube.NewService: %w", err)
	}

	Config.YtPlaylistId, err = ytgetplaylistid(Config.YtUsername, Config.YtChannelId)
	if err != nil {
		tglog("ERROR get youtube playlist id: %w", err)
		return fmt.Errorf("get youtube playlist id: %w", err)
	}
	if Config.YtPlaylistId == "" {
		tglog("ERROR YtPlaylistId empty")
		return fmt.Errorf("YtPlaylistId empty")
	}

	if Config.DEBUG {
		log("DEBUG channel id: %s", Config.YtChannelId)
		log("DEBUG playlist id: %s", Config.YtPlaylistId)
	}

	// https://pkg.go.dev/google.golang.org/api/youtube/v3#PlaylistItemSnippet
	var ytvideosids []string
	ytvideosids, err = ytplaylistitemslist(Config.YtPlaylistId, Config.YtLastPublishedAt)
	if err != nil {
		tglog("WARNING youtube list published: %w", err)
	}

	var ytvideos []youtube.Video
	if len(ytvideosids) > 0 {
		ytvideos, err = ytvideoslist(ytvideosids)
		if err != nil {
			tglog("WARNING youtube list published: %s", err)
		}
	}

	if Config.DEBUG {
		for j, v := range ytvideos {
			tglog(
				"DEBUG "+NL+"%d/%d "+NL+"«%s» "+NL+"youtu.be/%s "+NL+"%s "+NL+"liveStreamingDetails:%+v ",
				j+1,
				len(ytvideos),
				v.Snippet.Title,
				v.Id,
				v.Snippet.PublishedAt,
				v.LiveStreamingDetails,
			)
		}
	}

	for _, v := range ytvideos {

		if v.Snippet.PublishedAt <= Config.YtLastPublishedAt {

			// skip
			tglog("skipping video: %s %s<=%s", v.Id, v.Snippet.PublishedAt, Config.YtLastPublishedAt)

			Config.YtLastPublishedAt = v.Snippet.PublishedAt

			err = Config.Put()
			if err != nil {
				log("ERROR Config.Put: %s", err)
			}

		} else if v.LiveStreamingDetails == nil || v.LiveStreamingDetails.ActualEndTime != "" {

			// published

			err = tgpostpublished(v)
			if err != nil {
				tglog("ERROR telegram post published youtube video: %w", err)
				return fmt.Errorf("telegram post published youtube video: %w", err)
			}

			Config.YtLastPublishedAt = v.Snippet.PublishedAt

			err = Config.Put()
			if err != nil {
				log("ERROR Config.Put: %s", err)
			}

		} else if v.LiveStreamingDetails.ActualStartTime != "" && v.LiveStreamingDetails.ActualEndTime == "" {

			// live in progress

		} else {

			// live

			if v, err := time.Parse(v.LiveStreamingDetails.ScheduledStartTime, time.RFC3339); err == nil {
				Config.YtNextLive = v
			} else {
				log("ERROR parse LiveStreamingDetails.ScheduledStartTime: %s", err)
			}
			Config.YtNextLiveId = v.Id
			Config.YtNextLiveTitle = v.Snippet.Title
			Config.YtNextLiveReminderSent = false

			err = Config.Put()
			if err != nil {
				log("ERROR Config.Put: %s", err)
			}

			err = tgpostnextlive(v)
			if err != nil {
				tglog("telegram post next live: %w", err)
				return fmt.Errorf("telegram post next live: %w", err)
			}

			Config.YtLastPublishedAt = v.Snippet.PublishedAt

			err = Config.Put()
			if err != nil {
				log("ERROR Config.Put: %s", err)
			}
		}

	}

	return nil
}

func log(msg string, args ...interface{}) {
	t := time.Now().Local()
	ts := fmt.Sprintf(
		"%03d%02d%02d:"+"%02d%02d",
		t.Year()%1000, t.Month(), t.Day(), t.Hour(), t.Minute(),
	)
	fmt.Fprintf(os.Stderr, ts+" "+msg+NL, args...)
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
		ChatId:              Config.TgBossChatId,
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
	tgapiurl := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", Config.TgToken)
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
		fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", Config.TgToken),
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
		fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", Config.TgToken),
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
	if Config.DEBUG {
		log("DEBUG videos.list response: %+v", rv)
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
	var photourl string
	if ytvideo.Snippet.Thumbnails != nil {
		photourl = ytvideoPhotoUrl(*ytvideo.Snippet.Thumbnails)
	}

	if Config.DEBUG {
		log("DEBUG photourl: %s"+NL, photourl)
	}

	caption := fmt.Sprintf(
		TgLangMessages[Config.TgLang]["published"]+" "+NL+
			"*%s* "+NL+
			"https://youtu.be/%s "+NL,
		tgEscape(ytvideo.Snippet.Title),
		tgEscape(ytvideo.Id),
	)

	if Config.DEBUG {
		log("DEBUG tgpostpublished photo caption: "+NL+"%s"+NL, caption)
	}

	msg, err := tgSendPhoto(Config.TgChatId, photourl, caption)
	if err != nil {
		return fmt.Errorf("telegram send photo: %w", err)
	}

	log("posted telegram photo message id:%s"+NL, msg.Id)

	return nil
}

func tgpostnextlive(ytvideo youtube.Video) error {
	var err error

	var photourl string
	if ytvideo.Snippet.Thumbnails != nil {
		photourl = ytvideoPhotoUrl(*ytvideo.Snippet.Thumbnails)
	}

	if Config.DEBUG {
		log("DEBUG tgpostnextlive photourl: %s"+NL, photourl)
	}

	caption := fmt.Sprintf(
		TgLangMessages[Config.TgLang]["nextlive"]+" "+NL+
			"*%s* "+NL+
			"*%s/%d %s* (%s) "+NL+
			"https://youtu.be/%s "+NL,
		tgEscape(Config.YtNextLiveTitle),
		strings.ToTitle(monthnameru(Config.YtNextLive.In(TgTimezone).Month())),
		Config.YtNextLive.In(TgTimezone).Day(),
		Config.YtNextLive.In(TgTimezone).Format("15:04"),
		Config.TgTimezoneNameShort,
		tgEscape(Config.YtNextLiveId),
	)

	if Config.DEBUG {
		log("DEBUG tgpostnextlive photo caption: "+NL+"%s"+NL, caption)
	}

	msg, err := tgSendPhoto(Config.TgChatId, photourl, caption)
	if err != nil {
		return fmt.Errorf("telegram send photo: %w", err)
	}

	log("posted telegram photo message id:%s"+NL, msg.Id)

	return nil
}

func tgpostlivereminder() error {
	var err error

	text := fmt.Sprintf(
		TgLangMessages[Config.TgLang]["livereminder"]+" "+NL+
			"*%s* "+NL+
			"https://youtu.be/%s "+NL,
		tgEscape(Config.YtNextLiveTitle),
		tgEscape(Config.YtNextLiveId),
	)

	if Config.DEBUG {
		log("DEBUG tgpostlivereminder text: "+NL+"%s"+NL, text)
	}

	msg, err := tgSendMessage(Config.TgChatId, text)
	if err != nil {
		return fmt.Errorf("telegram send message: %w", err)
	}

	log("posted telegram text message id:%s"+NL, msg.Id)

	return nil
}

func (config *TgTubeNotiConfig) Get() error {
	req, err := http.NewRequest(http.MethodGet, config.YssUrl, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("yss response status %s", resp.Status)
	}

	rbb, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(rbb, config); err != nil {
		return err
	}

	if config.DEBUG {
		log("DEBUG Config.Get: %+v", config)
	}

	return nil
}

func (config *TgTubeNotiConfig) Put() error {
	if config.DEBUG {
		log("DEBUG Config.Put %s %+v", config.YssUrl, config)
	}

	rbb, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, config.YssUrl, bytes.NewBuffer(rbb))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("yss response status %s", resp.Status)
	}

	return nil
}
