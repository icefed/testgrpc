package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	pb "testgrpc/fileserver"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	Size8GB           = 8 * 1024 * 1024 * 1024
	MaxUploadFileSize = Size8GB
	Size4MB           = 4 * 1024 * 1024
	port              = ":50051"
	timestampFormat   = time.StampNano
)

// server is used to implement fileserver.GreeterServer.
type server struct {
	Dir string
}

// SayHello implements fileserver.GreeterServer
func (s *server) SayHello(ctx context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	log.Printf("Received: %v", in.Name)
	return &pb.HelloReply{Message: "Hello " + in.Name}, nil
}

// List ...
func (s *server) List(ctx context.Context, in *pb.Empty) (*pb.FileInfoResponse, error) {
	filesInfo := make([]*pb.FileInfo, 0)
	files, err := ioutil.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		fileinfo := pb.FileInfo{
			Name:  f.Name(),
			Size:  f.Size(),
			IsDir: f.IsDir(),
		}
		filesInfo = append(filesInfo, &fileinfo)
	}

	return &pb.FileInfoResponse{Files: filesInfo}, nil
}

// Upload ...
func (s *server) Upload(stream pb.Greeter_UploadServer) error {
	// Read metadata from client.
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.DataLoss, "ClientStreamingEcho: failed to get metadata")
	}
	filename := md["filename"][0]
	filesize, _ := strconv.Atoi(md["size"][0])
	log.Printf("Received Upload name: %v, size: %v", filename, filesize)

	// while there are messages coming
	size := 0
	file, err := os.OpenFile(path.Join(s.Dir, filename), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		data, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Println(err)
			return errors.Wrapf(err, "failed unexpectadely while reading chunks from stream")
		}
		size += len(data.Content)
		file.Write(data.Content)
	}
	statusCode := pb.StatusCode_Ok
	// check received data size
	if size != filesize {
		statusCode = pb.StatusCode_Failed
	}
	// once the transmission finished, send the
	// confirmation if nothign went wrong
	err = stream.SendAndClose(&pb.Status{
		Code: statusCode,
	})
	return err
}

// Download ...
func (s *server) Download(req *pb.FileName, stream pb.Greeter_DownloadServer) error {
	log.Printf("Received Download")
	filepath := path.Join(s.Dir, req.Name)
	stats, err := os.Stat(filepath)
	if err != nil {
		log.Printf("could not greet: %v", err)
	}
	// Create and send header.
	header := metadata.New(map[string]string{"filename": req.Name,
		"timestamp": time.Now().Format(timestampFormat),
		"size":      strconv.Itoa(int(stats.Size()))})
	stream.SendHeader(header)

	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer file.Close()
	buf := make([]byte, Size4MB)
	for {
		// put as many bytes as `chunkSize` into the buf array.
		n, err := file.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return errors.Wrapf(err, "failed unexpectadely while reading chunks from stream")
		}

		err = stream.Send(&pb.Chunk{
			// because we might've read less than
			// `chunkSize` we want to only send up to
			// `n` (amount of bytes read).
			// note: slicing (`:n`) won't copy the
			// underlying data, so this as fast as taking
			// a "pointer" to the underlying storage.
			Content: buf[:n],
		})
		if err != nil {
			return errors.Wrapf(err, "failed unexpectadely while sending chunks to stream")
		}
	}
	return nil
}

func main() {
	dir := "./"
	if len(os.Args) >= 2 {
		dir = os.Args[1]
	}
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer(grpc.MaxRecvMsgSize(MaxUploadFileSize))
	pb.RegisterGreeterServer(s, &server{
		Dir: dir,
	})
	fmt.Println("listen ", port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
