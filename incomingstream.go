package mediaserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/chuckpreslar/emission"
	"github.com/notedit/media-server-go/sdp"
)

type IncomingStream struct {
	id        string
	info      *sdp.StreamInfo
	transport DTLSICETransport
	receiver  RTPReceiverFacade
	tracks    map[string]*IncomingStreamTrack
	*emission.Emitter
}

func newIncomingStream(transport DTLSICETransport, receiver RTPReceiverFacade, info *sdp.StreamInfo) *IncomingStream {
	stream := &IncomingStream{}
	stream.id = info.GetID()
	stream.transport = transport
	stream.receiver = receiver
	stream.tracks = make(map[string]*IncomingStreamTrack)
	stream.Emitter = emission.NewEmitter()

	for _, track := range info.GetTracks() {
		stream.CreateTrack(track)
	}

	return stream
}

func (i *IncomingStream) GetID() string {
	return i.id
}

func (i *IncomingStream) GetStreamInfo() *sdp.StreamInfo {

	info := sdp.NewStreamInfo(i.id)

	for _, track := range i.tracks {
		info.AddTrack(track.GetTrackInfo().Clone())
	}
	return info
}

func (i *IncomingStream) GetStats() map[string]map[string]*IncomingAllStats {

	stats := map[string]map[string]*IncomingAllStats{}

	for _, track := range i.tracks {
		stats[track.GetID()] = track.GetStats()
	}

	return stats
}

func (i *IncomingStream) GetTrack(trackID string) *IncomingStreamTrack {
	return i.tracks[trackID]
}

func (i *IncomingStream) GetTracks() []*IncomingStreamTrack {
	tracks := []*IncomingStreamTrack{}
	for _, track := range i.tracks {
		tracks = append(tracks, track)
	}
	return tracks
}

func (i *IncomingStream) GetAudioTracks() []*IncomingStreamTrack {
	audioTracks := []*IncomingStreamTrack{}
	for _, track := range i.tracks {
		if strings.ToLower(track.GetMedia()) == "audio" {
			audioTracks = append(audioTracks, track)
		}
	}
	return audioTracks
}

func (i *IncomingStream) GetVideoTracks() []*IncomingStreamTrack {
	videoTracks := []*IncomingStreamTrack{}
	for _, track := range i.tracks {
		if strings.ToLower(track.GetMedia()) == "video" {
			videoTracks = append(videoTracks, track)
		}
	}
	return videoTracks
}

func (i *IncomingStream) AddTrack(track *IncomingStreamTrack) error {

	if _, ok := i.tracks[track.GetID()]; ok {
		return errors.New("Track id already present in stream")
	}

	track.Once("stopped", func() {
		delete(i.tracks, track.GetID())
	})

	i.tracks[track.GetID()] = track
	return nil
}

func (i *IncomingStream) CreateTrack(track *sdp.TrackInfo) *IncomingStreamTrack {

	var mediaType MediaFrameType = 0
	if track.GetMedia() == "video" {
		mediaType = 1
	}

	sources := map[string]RTPIncomingSourceGroup{}

	encodings := track.GetEncodings()

	if len(encodings) > 0 {

		for _, items := range encodings {

			for _, encoding := range items {

				source := NewRTPIncomingSourceGroup(mediaType)

				mid := track.GetMediaID()

				rid := encoding.GetID()

				source.SetRid(NewStringFacade(rid))

				if mid != "" {
					source.SetMid(NewStringFacade(mid))
				}

				params := encoding.GetParams()

				if ssrc, ok := params["ssrc"]; ok {
					ssrcUint, err := strconv.ParseUint(ssrc, 10, 32)
					if err != nil {
						fmt.Println("ssrc parse error ", err)
						continue
					}
					source.GetMedia().SetSsrc(uint(ssrcUint))
					groups := track.GetSourceGroupS()
					for _, group := range groups {
						// check if it is from us
						if group.GetSSRCs() != nil && group.GetSSRCs()[0] == source.GetMedia().GetSsrc() {
							if group.GetSemantics() == "FID" {
								source.GetRtx().SetSsrc(group.GetSSRCs()[1])
							}

							if group.GetSemantics() == "FEC-FR" {
								source.GetFec().SetSsrc(group.GetSSRCs()[1])
							}
						}
					}
				}

				i.transport.AddIncomingSourceGroup(source)
				sources[rid] = source
			}
		}

	} else if track.GetSourceGroup("SIM") != nil {
		// chrome like simulcast
		SIM := track.GetSourceGroup("SIM")

		ssrcs := SIM.GetSSRCs()

		groups := track.GetSourceGroupS()

		for j, ssrc := range ssrcs {

			source := NewRTPIncomingSourceGroup(mediaType)

			source.GetMedia().SetSsrc(ssrc)

			for _, group := range groups {

				if group.GetSSRCs()[0] == ssrc {

					if group.GetSemantics() == "FID" {
						source.GetRtx().SetSsrc(group.GetSSRCs()[1])
					}

					if group.GetSemantics() == "FEC-FR" {
						source.GetFec().SetSsrc(group.GetSSRCs()[1])
					}
				}
			}

			i.transport.AddIncomingSourceGroup(source)

			sources[strconv.Itoa(j)] = source
		}

	} else {
		source := NewRTPIncomingSourceGroup(mediaType)

		source.GetMedia().SetSsrc(track.GetSSRCS()[0])

		fid := track.GetSourceGroup("FID")
		fec_fr := track.GetSourceGroup("FEC-FR")

		if fid != nil {
			source.GetRtx().SetSsrc(fid.GetSSRCs()[1])
		} else {
			source.GetRtx().SetSsrc(0)
		}

		if fec_fr != nil {
			source.GetFec().SetSsrc(fec_fr.GetSSRCs()[1])
		} else {
			source.GetFec().SetSsrc(0)
		}

		i.transport.AddIncomingSourceGroup(source)

		// Append to soruces with empty rid
		sources[""] = source
	}

	incomingTrack := newIncomingStreamTrack(track.GetMedia(), track.GetID(), i.receiver, sources)

	incomingTrack.Once("stopped", func() {

		delete(i.tracks, incomingTrack.GetID())

		for _, source := range sources {
			i.transport.RemoveIncomingSourceGroup(source)
		}
	})

	i.tracks[track.GetID()] = incomingTrack

	i.EmitSync("track", incomingTrack)

	return incomingTrack
}

func (i *IncomingStream) Stop() {

	if i.transport == nil {
		return
	}

	for k, track := range i.tracks {
		track.Stop()
		delete(i.tracks, k)
	}

	i.Emit("stopped")

	i.transport = nil
}
