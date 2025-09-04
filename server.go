package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type StartRequest struct {
	IngestURL string `json:"ingestUrl"`
	VideoPort int    `json:"videoPort"`
	AudioPort int    `json:"audioPort"`
}

var (
	mu      sync.Mutex
	running bool
	pc      *webrtc.PeerConnection
)

func main() {
	http.HandleFunc("/start", startHandler)
	http.HandleFunc("/shutdown", shutdownHandler)

	log.Println("Pion WHIP relay server running on :8084")
	log.Fatal(http.ListenAndServe(":8084", nil))
}

func startHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()

	if running {
		http.Error(w, "already running", http.StatusConflict)
		return
	}

	var req StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Create PeerConnection
	m := webrtc.MediaEngine{}

	// Register Opus for audio
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		http.Error(w, "failed to register audio codec", 500)
		return
	}

	// Register H264 for video
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		http.Error(w, "failed to register video codec", 500)
		return
	}

	// Construct API
	api := webrtc.NewAPI(webrtc.WithMediaEngine(&m))
	var err error
	pc, err = api.NewPeerConnection(webrtc.Configuration{})

	if err != nil {
		http.Error(w, "failed to create pc", 500)
		return
	}

	// Create tracks and bind ports
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "pion-audio",
	)

	if err != nil {
		http.Error(w, "failed audio track", 500)
		return
	}

	pc.AddTrack(audioTrack)
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "pion-video",
	)

	if err != nil {
		http.Error(w, "failed video track", 500)
		return
	}

	pc.AddTrack(videoTrack)

	// Listen for RTP from ffmpeg
	go listenRTP(req.AudioPort, audioTrack)
	go listenRTP(req.VideoPort, videoTrack)

	// Create livekit offer
	offer, err := pc.CreateOffer(nil)
	// fmt.Printf("SDP OFFER: %s\n", offer.SDP)
	if err != nil {
		http.Error(w, "failed to create offer", 500)
		return
	}
	if err = pc.SetLocalDescription(offer); err != nil {
		http.Error(w, "failed to set local desc", 500)
		return
	}

	// Send offer to livekit
	reqBody := strings.NewReader(offer.SDP)
	httpReq, err := http.NewRequest("POST", req.IngestURL, reqBody)
	if err != nil {
		http.Error(w, "failed to build whip request", 500)
		return
	}
	httpReq.Header.Set("Content-Type", "application/sdp")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		http.Error(w, "whip request failed", 500)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("whip error %d: %s", resp.StatusCode, string(b)), 500)
		return
	}

	answerSDP, err := io.ReadAll(resp.Body)
	// fmt.Printf("SDP ANSWER: %s\n", string(answerSDP))
	if err != nil {
		http.Error(w, "failed to read whip answer", 500)
		return
	}

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answerSDP),
	}
	if err = pc.SetRemoteDescription(answer); err != nil {
		http.Error(w, "failed to set remote desc", 500)
		return
	}

	log.Printf("Starting relay: Ingest=%s video=%d audio=%d",
		req.IngestURL, req.VideoPort, req.AudioPort)

	running = true
	w.Write([]byte("Relay started"))
}

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Shutting down Pion server")
	w.Write([]byte("Relay server shutting down"))
	go shutdown()
}

func shutdown() {
	mu.Lock()
	running = false
	mu.Unlock()
	os.Exit(0)
}

func listenRTP(port int, track *webrtc.TrackLocalStaticRTP) {
	addr := net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		log.Printf("failed to listen on UDP %d: %v", port, err)
		return
	}
	defer conn.Close()

	log.Printf("Listening for RTP on udp://127.0.0.1:%d", port)

	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			log.Println("RTP read error:", err)
			return
		}

		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			log.Println("RTP unmarshal error:", err)
			continue
		}

		if err = track.WriteRTP(&pkt); err != nil {
			log.Println("RTP write error:", err)
			return
		}

		// log.Printf("Got RTP packet: SSRC=%d Seq=%d TS=%d Size=%d",
		// 	pkt.SSRC, pkt.SequenceNumber, pkt.Timestamp, len(pkt.Payload))
	}
}
