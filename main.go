// caktus-feed bridge: reads the WhatsApp Channel(s) this burner account follows
// and saves each post as one JSON line in feed/YYYY-MM-DD.jsonl, then pushes
// the feed folder to GitHub so Claude can read it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

var (
	phone     = flag.String("phone", "", "burner number with country code, digits only (only needed the first time, to link)")
	match     = flag.String("match", "", "only save channels whose name contains this text (blank = all followed channels)")
	pollEvery = flag.Duration("poll", 60*time.Second, "how often to check the channel for new posts")
	pushEvery = flag.Duration("push", 10*time.Minute, "how often to push new posts to GitHub")
	noPush    = flag.Bool("nopush", false, "save posts locally but don't push to GitHub")
	listOnly  = flag.Bool("list", false, "print followed channels and exit")
)

type post struct {
	Time    string `json:"time"`
	Channel string `json:"channel"`
	ID      int    `json:"id"`
	Text    string `json:"text"`
	Type    string `json:"type,omitempty"`
	Views   int    `json:"views,omitempty"`
}

const stateFile = "state.json"

func loadState() map[string]int {
	st := map[string]int{}
	if b, err := os.ReadFile(stateFile); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(st map[string]int) {
	b, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(stateFile, b, 0o644)
}

func textOf(m *waE2E.Message) (string, string) {
	if m == nil {
		return "", ""
	}
	switch {
	case m.GetConversation() != "":
		return m.GetConversation(), "text"
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetText(), "text"
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetCaption(), "image"
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetCaption(), "video"
	case m.GetDocumentMessage() != nil:
		d := m.GetDocumentMessage()
		return strings.TrimSpace(d.GetTitle() + " " + d.GetCaption()), "document"
	case m.GetPollCreationMessage() != nil:
		return m.GetPollCreationMessage().GetName(), "poll"
	}
	return "", "other"
}

func appendPost(p post) error {
	if err := os.MkdirAll("feed", 0o755); err != nil {
		return err
	}
	day := time.Now().Format("2006-01-02")
	f, err := os.OpenFile(filepath.Join("feed", day+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(p)
	_, err = f.Write(append(b, '\n'))
	return err
}

func poll(ctx context.Context, cli *whatsmeow.Client, st map[string]int) {
	chans, err := cli.GetSubscribedNewsletters(ctx)
	if err != nil {
		fmt.Println("couldn't list channels:", err)
		return
	}
	saved := 0
	for _, ch := range chans {
		name := ch.ThreadMeta.Name.Text
		if *match != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(*match)) {
			continue
		}
		key := ch.ID.String()
		msgs, err := cli.GetNewsletterMessages(ctx, ch.ID, &whatsmeow.GetNewsletterMessagesParams{Count: 50})
		if err != nil {
			fmt.Printf("couldn't fetch %s: %v\n", name, err)
			continue
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].MessageServerID < msgs[j].MessageServerID })
		for _, m := range msgs {
			if m.MessageServerID <= st[key] {
				continue
			}
			txt, typ := textOf(m.Message)
			if err := appendPost(post{
				Time: m.Timestamp.Format(time.RFC3339), Channel: name, ID: m.MessageServerID,
				Text: txt, Type: typ, Views: m.ViewsCount,
			}); err != nil {
				fmt.Println("couldn't save post:", err)
				return
			}
			st[key] = m.MessageServerID
			saved++
		}
	}
	if saved > 0 {
		saveState(st)
		fmt.Printf("%s saved %d new post(s)\n", time.Now().Format("15:04:05"), saved)
	}
}

func git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func push() {
	out, _ := exec.Command("git", "status", "--porcelain", "feed").Output()
	if len(strings.TrimSpace(string(out))) == 0 {
		return
	}
	_ = git("add", "feed")
	_ = git("commit", "-q", "-m", "feed "+time.Now().Format("2006-01-02 15:04"))
	if err := git("pull", "-q", "--rebase"); err != nil {
		fmt.Println("git pull failed, will retry:", err)
		_ = git("rebase", "--abort")
		return
	}
	if err := git("push", "-q"); err != nil {
		fmt.Println("git push failed, will retry:", err)
	}
}

func main() {
	flag.Parse()
	ctx := context.Background()
	log := waLog.Stdout("WA", "WARN", true)

	container, err := sqlstore.New(ctx, "sqlite3", "file:store.db?_foreign_keys=on", log)
	if err != nil {
		panic(err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		panic(err)
	}
	cli := whatsmeow.NewClient(device, log)

	connected := make(chan struct{}, 1)
	cli.AddEventHandler(func(evt interface{}) {
		switch evt.(type) {
		case *events.Connected:
			select {
			case connected <- struct{}{}:
			default:
			}
		case *events.LoggedOut:
			fmt.Println("WhatsApp logged this device out. Delete store.db and link again with -phone.")
			os.Exit(2)
		}
	})

	if cli.Store.ID == nil {
		if *phone == "" {
			fmt.Println("Not linked yet. Run again with -phone 1XXXXXXXXXX (your burner number).")
			os.Exit(1)
		}
		qr, _ := cli.GetQRChannel(ctx)
		if err := cli.Connect(); err != nil {
			panic(err)
		}
		<-qr // wait for the connection to be ready
		code, err := cli.PairPhone(ctx, *phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if err != nil {
			panic(err)
		}
		fmt.Println("\n=== In WhatsApp: Settings > Linked devices > Link a device > Link with phone number instead ===")
		fmt.Println("=== Enter this code:", code, "===")
		for item := range qr {
			if item == whatsmeow.QRChannelSuccess {
				fmt.Println("Linked!")
				break
			}
			if item == whatsmeow.QRChannelTimeout {
				fmt.Println("Code expired. Run the same command again for a new one.")
				os.Exit(1)
			}
		}
	} else if err := cli.Connect(); err != nil {
		panic(err)
	}

	select {
	case <-connected:
	case <-time.After(60 * time.Second):
		fmt.Println("Still waiting on WhatsApp to connect...")
		<-connected
	}
	fmt.Println("Connected.")

	if *listOnly {
		chans, err := cli.GetSubscribedNewsletters(ctx)
		if err != nil {
			panic(err)
		}
		for _, ch := range chans {
			fmt.Printf("- %s (%s)\n", ch.ThreadMeta.Name.Text, ch.ID)
		}
		cli.Disconnect()
		return
	}

	st := loadState()
	poll(ctx, cli, st)
	if !*noPush {
		push()
	}

	pollT := time.NewTicker(*pollEvery)
	pushT := time.NewTicker(*pushEvery)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case <-pollT.C:
			if cli.IsConnected() {
				poll(ctx, cli, st)
			}
		case <-pushT.C:
			if !*noPush {
				push()
			}
		case <-stop:
			fmt.Println("Stopping.")
			if !*noPush {
				push()
			}
			cli.Disconnect()
			return
		}
	}
}
