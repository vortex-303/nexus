package vectorstore

import (
	"context"
	"fmt"
	"time"

	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Result is a single search result from the vector store.
type Result struct {
	ID    string
	Score float32
}

// VectorStore wraps a Qdrant gRPC client.
type VectorStore struct {
	conn    *grpc.ClientConn
	points  pb.PointsClient
	collect pb.CollectionsClient
}

// New connects to a Qdrant instance at the given URL (host:port).
func New(url string) (*VectorStore, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to qdrant at %s: %w", url, err)
	}

	return &VectorStore{
		conn:    conn,
		points:  pb.NewPointsClient(conn),
		collect: pb.NewCollectionsClient(conn),
	}, nil
}

// Close closes the gRPC connection.
func (v *VectorStore) Close() {
	if v.conn != nil {
		v.conn.Close()
	}
}

// collectionName returns the collection name for a workspace.
func collectionName(slug string) string {
	return "knowledge_" + slug
}

// EnsureCollection creates the collection if it doesn't exist.
func (v *VectorStore) EnsureCollection(slug string, dimension int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	name := collectionName(slug)

	// Check if exists
	_, err := v.collect.Get(ctx, &pb.GetCollectionInfoRequest{CollectionName: name})
	if err == nil {
		return nil // already exists
	}

	dist := pb.Distance_Cosine
	size := uint64(dimension)
	_, err = v.collect.Create(ctx, &pb.CreateCollection{
		CollectionName: name,
		VectorsConfig: &pb.VectorsConfig{
			Config: &pb.VectorsConfig_Params{
				Params: &pb.VectorParams{
					Size:     size,
					Distance: dist,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating collection %s: %w", name, err)
	}
	return nil
}

// Upsert inserts or updates a vector with a payload.
func (v *VectorStore) Upsert(slug, id string, vector []float32, payload map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := v.EnsureCollection(slug, len(vector)); err != nil {
		return err
	}

	// Build payload
	pbPayload := make(map[string]*pb.Value)
	for k, val := range payload {
		switch tv := val.(type) {
		case string:
			pbPayload[k] = &pb.Value{Kind: &pb.Value_StringValue{StringValue: tv}}
		}
	}

	wait := true
	_, err := v.points.Upsert(ctx, &pb.UpsertPoints{
		CollectionName: collectionName(slug),
		Wait:           &wait,
		Points: []*pb.PointStruct{
			{
				Id: &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: id}},
				Vectors: &pb.Vectors{
					VectorsOptions: &pb.Vectors_Vector{
						Vector: &pb.Vector{Data: vector},
					},
				},
				Payload: pbPayload,
			},
		},
	})
	return err
}

// Search finds the top-N most similar vectors.
func (v *VectorStore) Search(slug string, vector []float32, limit int) ([]Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := v.points.Search(ctx, &pb.SearchPoints{
		CollectionName: collectionName(slug),
		Vector:         vector,
		Limit:          uint64(limit),
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: false}},
	})
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(resp.Result))
	for _, r := range resp.Result {
		id := ""
		if uid := r.Id.GetUuid(); uid != "" {
			id = uid
		}
		results = append(results, Result{ID: id, Score: r.Score})
	}
	return results, nil
}

// Delete removes a vector by ID.
func (v *VectorStore) Delete(slug, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wait := true
	_, err := v.points.Delete(ctx, &pb.DeletePoints{
		CollectionName: collectionName(slug),
		Wait:           &wait,
		Points: &pb.PointsSelector{
			PointsSelectorOneOf: &pb.PointsSelector_Points{
				Points: &pb.PointsIdsList{
					Ids: []*pb.PointId{
						{PointIdOptions: &pb.PointId_Uuid{Uuid: id}},
					},
				},
			},
		},
	})
	return err
}
