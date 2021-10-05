package service

import (
	"context"
	"time"

	pb "github.com/textileio/bidbot/gen/v1"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/service/comm"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type progressReporter struct {
	comm comm.Comm
	ctx  context.Context
}

func (pr progressReporter) StartFetching(bidID auction.BidID, attempts uint32) {
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_StartFetching_{StartFetching: &pb.BidbotEvent_StartFetching{
			BidId:    string(bidID),
			Attempts: attempts,
		}},
	}
	pr.comm.PublishBidbotEvent(pr.ctx, event)
}
func (pr progressReporter) ErrorFetching(bidID auction.BidID, attempts uint32, err error) {
	if err == nil {
		return
	}
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_ErrorFetching_{ErrorFetching: &pb.BidbotEvent_ErrorFetching{
			BidId:    string(bidID),
			Attempts: attempts,
			Error:    err.Error(),
		}},
	}
	pr.comm.PublishBidbotEvent(pr.ctx, event)
}
func (pr progressReporter) StartImporting(bidID auction.BidID, attempts uint32) {
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_StartImporting_{StartImporting: &pb.BidbotEvent_StartImporting{
			BidId:    string(bidID),
			Attempts: attempts,
		}},
	}
	pr.comm.PublishBidbotEvent(pr.ctx, event)
}

func (pr progressReporter) EndImporting(bidID auction.BidID, attempts uint32, err error) {
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_EndImporting_{EndImporting: &pb.BidbotEvent_EndImporting{
			BidId:    string(bidID),
			Attempts: attempts,
			Error:    "",
		}},
	}
	if err != nil {
		event.Type.(*pb.BidbotEvent_EndImporting_).EndImporting.Error = err.Error()
	}

	pr.comm.PublishBidbotEvent(pr.ctx, event)
}

func (pr progressReporter) Finalized(bidID auction.BidID) {
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_Finalized_{Finalized: &pb.BidbotEvent_Finalized{
			BidId: string(bidID),
		}},
	}
	pr.comm.PublishBidbotEvent(pr.ctx, event)
}

func (pr progressReporter) Errored(bidID auction.BidID, errorCause string) {
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_Errored_{Errored: &pb.BidbotEvent_Errored{
			BidId:      string(bidID),
			ErrorCause: errorCause,
		}},
	}
	pr.comm.PublishBidbotEvent(pr.ctx, event)
}
