package submit

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// OutboundFor finds the outbound message a bounce or complaint refers to (P5.4): by its ID when the bounce
// carried one, else by the Message-ID header we generated. "" when nothing in the organisation matches.
func (r *Repo) OutboundFor(ctx context.Context, organizationID, outboundID, messageIDHeader string) (string, error) {
	var filter bson.M
	switch {
	case outboundID != "" && store.IsID(store.PrefixOutbound, outboundID):
		filter = bson.M{"_id": outboundID, "organizationId": organizationID}
	case messageIDHeader != "":
		filter = bson.M{"organizationId": organizationID, "messageIdHeader": messageIDHeader}
	default:
		return "", nil
	}
	var doc struct {
		ID string `bson:"_id"`
	}
	err := r.coll().FindOne(ctx, filter, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	return doc.ID, err
}
