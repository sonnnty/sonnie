package readaloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/Sonty15/SONNIE/internal/tts"
	"github.com/Sonty15/SONNIE/internal/voice"
)

func TestSessionVoices_RoundsWithoutRepeatsAndLoopsWithDeterministicRNG(t *testing.T) {
	var logBuf bytes.Buffer
	origLog := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origLog)

	e := newEnv(t)
	rng := rand.New(rand.NewSource(42))
	e.m.cfg.RandIntn = rng.Intn

	pool := tts.DefaultVoicePool
	n := len(pool)
	if n != 16 {
		t.Fatalf("expected 16 voices in pool, got %d", n)
	}

	// Round 1: 16 participants get all 16 distinct voices without repeats.
	var round1 []string
	for i := 0; i < n; i++ {
		user := fmt.Sprintf("r1u%d", i)
		res := e.mustJoin(user)
		if res.Voice == "" {
			t.Fatalf("user %s got empty voice", user)
		}
		if slices.Contains(round1, res.Voice) {
			t.Fatalf("duplicate voice %q in round 1 (so far: %v)", res.Voice, round1)
		}
		round1 = append(round1, res.Voice)
		wantLog := fmt.Sprintf("tts: session voice guild=g1 user=%s voice=%s round=1", user, res.Voice)
		if !strings.Contains(logBuf.String(), wantLog) {
			t.Fatalf("missing log %q in %q", wantLog, logBuf.String())
		}
	}
	gotSorted := slices.Clone(round1)
	slices.Sort(gotSorted)
	wantSorted := slices.Clone(pool)
	slices.Sort(wantSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("round 1 voices = %v, want permutation of %v", gotSorted, wantSorted)
	}
	if slices.Equal(round1, pool) {
		t.Fatalf("expected seeded RNG to shuffle pool, got unshuffled %v", round1)
	}

	// Round 2: 17th through 32nd participants start round 2 and again get all 16 voices without repeats.
	var round2 []string
	for i := 0; i < n; i++ {
		user := fmt.Sprintf("r2u%d", i)
		res := e.mustJoin(user)
		if slices.Contains(round2, res.Voice) {
			t.Fatalf("duplicate voice %q in round 2 (so far: %v)", res.Voice, round2)
		}
		round2 = append(round2, res.Voice)
		wantLog := fmt.Sprintf("tts: session voice guild=g1 user=%s voice=%s round=2", user, res.Voice)
		if !strings.Contains(logBuf.String(), wantLog) {
			t.Fatalf("missing log %q", wantLog)
		}
	}
	gotSorted2 := slices.Clone(round2)
	slices.Sort(gotSorted2)
	if !slices.Equal(gotSorted2, wantSorted) {
		t.Fatalf("round 2 voices = %v, want permutation of %v", gotSorted2, wantSorted)
	}

	// 33rd participant starts round 3.
	res33 := e.mustJoin("r3u0")
	wantLog := fmt.Sprintf("tts: session voice guild=g1 user=r3u0 voice=%s round=3", res33.Voice)
	if !strings.Contains(logBuf.String(), wantLog) {
		t.Fatalf("missing round 3 log %q", wantLog)
	}
}

func TestSessionVoices_PersistsAcrossMoveLeaveAndAutoLeaveAndResetsOnNewSession(t *testing.T) {
	e := newEnv(t)
	e.m.cfg.VoicePool = []string{"Puck", "Orus", "Charon"}
	seq := 0
	e.m.cfg.RandIntn = func(n int) int {
		i := seq % n
		seq++
		return i
	}

	r1 := e.joinAt("u1", "t1")
	r2 := e.joinAt("u2", "t1")
	if r1.Voice != "Puck" || r2.Voice != "Charon" {
		t.Fatalf("got voices u1=%q u2=%q, want Puck and Charon", r1.Voice, r2.Voice)
	}

	// Repeat /tts join in the same channel without voice keeps the voice.
	again := e.joinAt("u1", "t1")
	if !again.AlreadyIn || again.Voice != "Puck" {
		t.Fatalf("repeat join: %+v", again)
	}

	// Moving to another text channel keeps the voice without advancing the round.
	moved := e.joinAt("u1", "t2")
	if !moved.Moved || moved.Voice != "Puck" {
		t.Fatalf("moved join: %+v", moved)
	}

	// /tts leave + rejoin keeps the voice.
	if _, err := e.m.Leave("g1", "u1"); err != nil {
		t.Fatal(err)
	}
	if v, ok := e.m.Voice("g1", "u1"); !ok || v != "Puck" {
		t.Fatalf("voice while left = %q %v, want Puck true", v, ok)
	}
	rejoined := e.joinAt("u1", "t1")
	if rejoined.Voice != "Puck" {
		t.Fatalf("rejoined after /tts leave got %q, want Puck", rejoined.Voice)
	}

	// Auto-leave on voice disconnect + rejoin keeps the voice.
	e.m.HandleUserVoice("g1", "u1", "")
	rejoinedAuto := e.joinAt("u1", "t1")
	if rejoinedAuto.Voice != "Puck" {
		t.Fatalf("rejoined after auto-leave got %q, want Puck", rejoinedAuto.Voice)
	}

	// Only 1 voice ("Orus") remains in round 1 because u1 kept "Puck".
	r3 := e.joinAt("u3", "t1")
	if r3.Voice != "Orus" {
		t.Fatalf("u3 got %q, want the last unused round-1 voice Orus", r3.Voice)
	}

	// Ending the session and starting a new one resets assignments.
	for _, u := range []string{"u1", "u2", "u3"} {
		if _, err := e.m.Leave("g1", u); err != nil {
			t.Fatal(err)
		}
	}
	e.m.Wait()
	seq = 1 // picks index 1 ("Orus") on the fresh pool
	fresh := e.joinAt("u1", "t1")
	if !fresh.Started || fresh.Voice != "Orus" {
		t.Fatalf("new session got %+v, want Started with Orus", fresh)
	}
}

func TestSessionVoices_PickOptionCountsInRoundAndCanChangeVoice(t *testing.T) {
	var logBuf bytes.Buffer
	origLog := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origLog)

	e := newEnv(t)
	e.m.cfg.VoicePool = []string{"Achird", "Puck", "Orus"}

	// u1 picks "Puck" on join: counts as used in round 1.
	res1, err := e.m.Join(context.Background(), JoinRequest{
		GuildID: "g1", UserID: "u1", VoiceChannelID: "v1", TextChannelID: "t1", Voice: "Puck",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res1.Voice != "Puck" {
		t.Fatalf("u1 voice = %q, want Puck", res1.Voice)
	}

	// u2 and u3 join randomly: must get the remaining two voices ("Achird" and "Orus") in round 1.
	res2 := e.joinAt("u2", "t1")
	res3 := e.joinAt("u3", "t1")
	if res2.Voice != "Achird" || res3.Voice != "Orus" {
		t.Fatalf("random joiners got %q and %q, want Achird and Orus", res2.Voice, res3.Voice)
	}

	// u1 picks "Orus" while already in t1: changes voice and edits the card.
	changed, err := e.m.Join(context.Background(), JoinRequest{
		GuildID: "g1", UserID: "u1", VoiceChannelID: "v1", TextChannelID: "t1", Voice: "Orus",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.VoiceChanged || changed.AlreadyIn || changed.Moved || changed.Voice != "Orus" {
		t.Fatalf("voice change result: %+v", changed)
	}
	if got := e.cardText("t1"); !strings.Contains(got, "<@u1> 🎙️Orus, <@u2> 🎙️Achird, <@u3> 🎙️Orus · <#t1>") {
		t.Fatalf("card after voice change: %q", got)
	}
	// Picking the same voice again in the same channel is AlreadyIn.
	same, err := e.m.Join(context.Background(), JoinRequest{
		GuildID: "g1", UserID: "u1", VoiceChannelID: "v1", TextChannelID: "t1", Voice: "Orus",
	})
	if err != nil || !same.AlreadyIn || same.VoiceChanged || same.Voice != "Orus" {
		t.Fatalf("same voice pick: %+v err=%v", same, err)
	}
	// Repeat /tts join without voice keeps "Orus".
	keep := e.joinAt("u1", "t1")
	if !keep.AlreadyIn || keep.Voice != "Orus" {
		t.Fatalf("repeat without voice: %+v", keep)
	}
}

func TestSessionVoices_PerMessageSynthesisUsesParticipantVoice(t *testing.T) {
	t.Run("REST Gemini and Chirp3-HD mapping", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			model      string
			wantVoices []string
		}{
			{"gemini", "gemini-2.5-flash-tts", []string{"Puck", "Orus"}},
			{"chirp3", "", []string{"th-TH-Chirp3-HD-Puck", "th-TH-Chirp3-HD-Orus"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var (
					mu     sync.Mutex
					voices []string
				)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					var req struct {
						Voice struct {
							Name string `json:"name"`
						} `json:"voice"`
					}
					_ = json.Unmarshal(body, &req)
					mu.Lock()
					voices = append(voices, req.Voice.Name)
					mu.Unlock()
					_, _ = w.Write([]byte(`{"audioContent":"` + base64.StdEncoding.EncodeToString([]byte("OggS")) + `"}`))
				}))
				defer srv.Close()

				e := newEnv(t)
				e.m.cfg.VoicePool = []string{"Puck", "Orus"}
				e.m.cfg.Synth = &tts.Google{
					APIKey:   "k",
					Endpoint: srv.URL,
					Client:   srv.Client(),
					Model:    tc.model,
					Voice:    "Charon",
				}

				e.joinAt("u1", "t1")
				e.joinAt("u2", "t1")
				e.say("u1", "ข้อความแรก")
				e.say("u2", "ข้อความสอง")
				recv(t, e.j.conn(0))
				recv(t, e.j.conn(0))

				mu.Lock()
				got := slices.Clone(voices)
				mu.Unlock()
				if !slices.Equal(got, tc.wantVoices) {
					t.Fatalf("synthesized voices = %v, want %v", got, tc.wantVoices)
				}
			})
		}
	})

	t.Run("gRPC streaming", func(t *testing.T) {
		var (
			mu     sync.Mutex
			voices []string
		)
		lis := bufconn.Listen(1 << 20)
		srv := grpc.NewServer()
		texttospeechpb.RegisterTextToSpeechServer(srv, &fakeStreamServer{
			onCall: func(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
				first, err := stream.Recv()
				if err != nil {
					return err
				}
				if _, err := stream.Recv(); err != nil {
					return err
				}
				if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
					return errors.New("expected EOF")
				}
				mu.Lock()
				voices = append(voices, first.GetStreamingConfig().GetVoice().GetName())
				mu.Unlock()
				return stream.Send(&texttospeechpb.StreamingSynthesizeResponse{AudioContent: []byte("audio")})
			},
		})
		go srv.Serve(lis)
		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			conn.Close()
			srv.Stop()
		})
		client := texttospeechpb.NewTextToSpeechClient(conn)

		e := newEnv(t)
		e.m.cfg.VoicePool = []string{"Fenrir", "Enceladus"}
		e.m.cfg.Encoder = voice.FakeStreamEncoder{}
		e.m.cfg.Synth = &tts.GoogleStream{
			REST: &tts.Google{Model: "gemini-2.5-flash-tts", Voice: "Charon"},
			Open: func(ctx context.Context) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error) {
				return client.StreamingSynthesize(ctx)
			},
		}

		e.joinAt("u1", "t1")
		e.joinAt("u2", "t1")
		e.say("u1", "หนึ่ง")
		e.say("u2", "สอง")
		recv(t, e.j.conn(0))
		recv(t, e.j.conn(0))

		mu.Lock()
		got := slices.Clone(voices)
		mu.Unlock()
		if !slices.Equal(got, []string{"Fenrir", "Enceladus"}) {
			t.Fatalf("streamed voices = %v, want [Fenrir Enceladus]", got)
		}
	})
}

type fakeStreamServer struct {
	texttospeechpb.UnimplementedTextToSpeechServer
	onCall func(texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error
}

func (s *fakeStreamServer) StreamingSynthesize(stream texttospeechpb.TextToSpeech_StreamingSynthesizeServer) error {
	return s.onCall(stream)
}
