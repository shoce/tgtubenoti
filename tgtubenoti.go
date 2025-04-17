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

	"github.com/shoce/tg"
)

const (
	NL   = "\n"
	SPAC = "    "
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

	YtEventType  string `yaml:"YtEventType"`  // = "upcoming" // = "completed"
	YtMaxResults int64  `yaml:"YtMaxResults"` // = 50

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

	LogTimeZone string

	HttpClient = &http.Client{}

	YtSvc *youtube.Service

	TgTimezone *time.Location

	// TODO TgLangMessages in Config
	TgLangMessages = map[string]map[string]string{
		"deutsch": map[string]string{
			"published":    "Neues Video",
			"nextlive":     "Bevorstehender Livestream",
			"livereminder": "Der Livestream beginnt in einer Stunde",
			"months":       "januar februar märz april mai juni juli august september oktober november dezember",
			"today":        "heute",
		},
		"english": map[string]string{
			"published":    "New video",
			"nextlive":     "Upcoming live",
			"livereminder": "Live starts in one hour",
			"months":       "january february march april may june july august september october november december",
			"today":        "today",
		},
		"french": map[string]string{
			"published":    "Nouveau vidéo",
			"nextlive":     "Prochain live",
			"livereminder": "Le live commence dans une heure",
			"months":       "janvier février mars avril mai juin juillet aout septembre octobre novembre décembre",
			"today":        "aujourd'hui",
		},
		"hindi": map[string]string{
			"published":    "नया वीडियो",
			"nextlive":     "आगामी लाइव",
			"livereminder": "लाइव एक घंटे में शुरू होगा",
			"months":       "जनवरी फरवरी मार्च अप्रैल मई जून जुलाई अगस्त सितम्बर अक्टूबर नवम्बर दिसम्बर",
			"today":        "आज",
		},
		"russian": map[string]string{
			"published":    "Новое видео",
			"nextlive":     "Запланированный эфир",
			"livereminder": "Через час начало эфира",
			"months":       "январь февраль март апрель май июнь июль август сентябрь октябрь ноябрь декабрь",
			"today":        "сегодня",
		},
		"spanish": map[string]string{
			"published":    "Nuevo video",
			"nextlive":     "Próximo en vivo",
			"livereminder": "El directo comienza en una hora",
			"months":       "enero febrero marzo abril mayo junio julio agosto septiembre octubre noviembre diciembre",
			"today":        "hoy",
		},
		"ukrainian": map[string]string{
			"published":    "Нове відео",
			"nextlive":     "Запланований ефір",
			"livereminder": "Через годину початок ефіру",
			"months":       "січень лютий березень квітень травень червень липень серпень вересень жовтень листопад грудень",
			"today":        "сьогодні",
		},
	}
)

func init() {
	var err error

	LogTimeZone = time.Now().Local().Format("-0700")
	LogTimeZone = strings.TrimRight(LogTimeZone, "0")

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

	if Config.DEBUG {
		log("DEBUG==true")
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

	tg.ApiToken = Config.TgToken

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
		tglog("ERROR time.LoadLocation `%s`: %v", Config.TgTimezoneName, err)
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

	if Config.YtMaxResults == 0 {
		log("ERROR YtMaxResults empty")
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
			tglog("ERROR CheckTube: %v", err)
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
		return fmt.Errorf("youtube.NewService: %w", err)
	}

	Config.YtPlaylistId, err = ytgetplaylistid(Config.YtUsername, Config.YtChannelId)
	if err != nil {
		return fmt.Errorf("get youtube playlist id: %w", err)
	}
	if Config.YtPlaylistId == "" {
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
		return fmt.Errorf("youtube playlist items list: %v", err)
	}

	var ytvideos []youtube.Video
	if len(ytvideosids) > 0 {
		ytvideos, err = ytvideoslist(ytvideosids)
		if err != nil {
			return fmt.Errorf("youtube videos list: %v", err)
		}
	}

	if Config.DEBUG {
		for j, v := range ytvideos {
			tglog(
				"DEBUG"+NL+"%d/%d"+" "+"%s"+NL+
					"youtu.be/%s"+" "+"%s"+NL+"liveStreamingDetails==%+v",
				j+1, len(ytvideos), v.Snippet.Title, v.Id,
				v.Snippet.PublishedAt, v.LiveStreamingDetails,
			)
		}
	}

	for _, v := range ytvideos {

		if v.Snippet.PublishedAt <= Config.YtLastPublishedAt {

			// skip
			tglog("skipping video %s %s", v.Id, v.Snippet.PublishedAt)

			Config.YtLastPublishedAt = v.Snippet.PublishedAt

			err = Config.Put()
			if err != nil {
				log("ERROR Config.Put: %s", err)
			}

		} else if v.LiveStreamingDetails == nil || v.LiveStreamingDetails.ActualEndTime != "" {

			// published

			err = tgpostpublished(v)
			if err != nil {
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

			if v, err := time.Parse(time.RFC3339, v.LiveStreamingDetails.ScheduledStartTime); err == nil {
				Config.YtNextLive = v
			} else {
				return fmt.Errorf("telegram post next live time.Parse ScheduledStartTime: %w", err)
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

	playlistitemslistcall := YtSvc.PlaylistItems.List([]string{"snippet", "contentDetails"}).MaxResults(Config.YtMaxResults)
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

	caption := tg.Esc(TgLangMessages[Config.TgLang]["published"]) + NL +
		tg.Bold(ytvideo.Snippet.Title) + NL +
		tg.Esc(fmt.Sprintf("https://youtu.be/%s", ytvideo.Id)) + NL

	if _, err := tg.SendPhoto(tg.SendPhotoRequest{
		ChatId:  Config.TgChatId,
		Photo:   photourl,
		Caption: caption,
	}); err != nil {
		return fmt.Errorf("telegram send photo: %w", err)
	}

	return nil
}

func tgpostnextlive(ytvideo youtube.Video) error {
	var err error

	var photourl string
	if ytvideo.Snippet.Thumbnails != nil {
		photourl = ytvideoPhotoUrl(*ytvideo.Snippet.Thumbnails)
	}

	caption := tg.Esc(TgLangMessages[Config.TgLang]["nextlive"]) + NL +
		tg.Bold(Config.YtNextLiveTitle) + NL +
		tg.Bold(fmt.Sprintf("%s/%d %s",
			strings.ToTitle(strings.Fields(TgLangMessages[Config.TgLang]["months"])[Config.YtNextLive.In(TgTimezone).Month()-1]),
			Config.YtNextLive.In(TgTimezone).Day(),
			Config.YtNextLive.In(TgTimezone).Format("15:04")),
		) + " " +
		tg.Esc(fmt.Sprintf("(%s)", Config.TgTimezoneNameShort)) + NL +
		tg.Esc(fmt.Sprintf("https://youtu.be/%s", Config.YtNextLiveId)) + NL

	if _, err = tg.SendPhoto(tg.SendPhotoRequest{
		ChatId:  Config.TgChatId,
		Photo:   photourl,
		Caption: caption,
	}); err != nil {
		return fmt.Errorf("telegram send photo: %w", err)
	}

	return nil
}

func tgpostlivereminder() error {
	var err error

	text := tg.Esc(TgLangMessages[Config.TgLang]["livereminder"]) + NL +
		tg.Bold(Config.YtNextLiveTitle) + NL +
		tg.Esc(fmt.Sprintf("https://youtu.be/%s", Config.YtNextLiveId)) + NL

	if Config.DEBUG {
		log("DEBUG tgpostlivereminder text: "+NL+"%s"+NL, text)
	}

	msg, err := tg.SendMessage(tg.SendMessageRequest{
		ChatId: Config.TgChatId,
		Text:   text,

		LinkPreviewOptions: tg.LinkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return fmt.Errorf("telegram send message: %w", err)
	}

	log("posted telegram text message id==%s"+NL, msg.Id)

	return nil
}

func ts() string {
	tnow := time.Now().Local()
	return fmt.Sprintf(
		"%d%02d%02d:%02d%02d%s",
		tnow.Year()%1000, tnow.Month(), tnow.Day(),
		tnow.Hour(), tnow.Minute(), LogTimeZone,
	)
}

func log(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, ts()+" "+msg+NL, args...)
}

func tglog(msg string, args ...interface{}) (err error) {
	log(msg, args...)
	text := fmt.Sprintf(msg, args...) + NL
	text = tg.Esc(text)
	_, err = tg.SendMessage(tg.SendMessageRequest{
		ChatId: Config.TgBossChatId,
		Text:   text,

		DisableNotification: true,
		LinkPreviewOptions:  tg.LinkPreviewOptions{IsDisabled: true},
	})
	return err
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
