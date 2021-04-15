package v1

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/odpf/optimus/datastore"

	"github.com/golang/protobuf/ptypes"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "github.com/odpf/optimus/api/proto/v1"
	"github.com/odpf/optimus/core/logger"
	log "github.com/odpf/optimus/core/logger"
	"github.com/odpf/optimus/core/progress"
	"github.com/odpf/optimus/job"
	"github.com/odpf/optimus/models"
	"github.com/odpf/optimus/store"
)

type ProjectRepoFactory interface {
	New() store.ProjectRepository
}

type JobRepoFactory interface {
	New(spec models.ProjectSpec) store.JobSpecRepository
}

type SecretRepoFactory interface {
	New(spec models.ProjectSpec) store.ProjectSecretRepository
}

type ProtoAdapter interface {
	FromJobProto(*pb.JobSpecification) (models.JobSpec, error)
	ToJobProto(models.JobSpec) (*pb.JobSpecification, error)

	FromProjectProto(*pb.ProjectSpecification) models.ProjectSpec
	ToProjectProto(models.ProjectSpec) *pb.ProjectSpecification

	FromInstanceProto(*pb.InstanceSpec) (models.InstanceSpec, error)
	ToInstanceProto(models.InstanceSpec) (*pb.InstanceSpec, error)

	FromResourceProto(res *pb.ResourceSpecification) (models.ResourceSpec, error)
	ToResourceProto(res models.ResourceSpec) (*pb.ResourceSpecification, error)
}

type RuntimeServiceServer struct {
	version            string
	jobSvc             models.JobService
	resourceSvc        models.DatastoreService
	adapter            ProtoAdapter
	projectRepoFactory ProjectRepoFactory
	secretRepoFactory  SecretRepoFactory
	instSvc            models.InstanceService
	scheduler          models.SchedulerUnit

	progressObserver progress.Observer
	Now              func() time.Time

	pb.UnimplementedRuntimeServiceServer
}

func (sv *RuntimeServiceServer) Version(ctx context.Context, version *pb.VersionRequest) (*pb.VersionResponse, error) {
	log.I(fmt.Printf("client with version %s requested for ping ", version.Client))
	response := &pb.VersionResponse{
		Server: sv.version,
	}
	return response, nil
}

func (sv *RuntimeServiceServer) DeployJobSpecification(req *pb.DeployJobSpecificationRequest, respStream pb.RuntimeService_DeployJobSpecificationServer) error {
	startTime := time.Now()

	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	var jobsToKeep []models.JobSpec
	for _, reqJob := range req.GetJobs() {
		adaptJob, err := sv.adapter.FromJobProto(reqJob)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("%s: cannot adapt job %s", err.Error(), reqJob.GetName()))
		}

		err = sv.jobSvc.Create(adaptJob, projSpec)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("%s: failed to save %s", err.Error(), adaptJob.Name))
		}
		jobsToKeep = append(jobsToKeep, adaptJob)
	}

	observers := new(progress.ObserverChain)
	observers.Join(sv.progressObserver)
	observers.Join(&jobSyncObserver{
		stream: respStream,
		log:    logrus.New(),
	})

	// delete specs not sent for deployment
	// currently we don't support deploying a single dag at a time so this will change
	// once we do that
	if err := sv.jobSvc.KeepOnly(projSpec, jobsToKeep, observers); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("%s: failed to delete jobs", err.Error()))
	}

	if err := sv.jobSvc.Sync(respStream.Context(), projSpec, observers); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("%s\nfailed to sync jobs", err.Error()))
	}

	logger.I("finished job deployment in", time.Since(startTime))
	return nil
}

func (sv *RuntimeServiceServer) ListJobSpecification(ctx context.Context, req *pb.ListJobSpecificationRequest) (*pb.ListJobSpecificationResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	jobSpecs, err := sv.jobSvc.GetAll(projSpec)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to retrive jobs for project %s", err.Error(), req.GetProjectName()))
	}

	jobProtos := []*pb.JobSpecification{}
	for _, jobSpec := range jobSpecs {
		jobProto, err := sv.adapter.ToJobProto(jobSpec)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse job spec %s", err.Error(), jobSpec.Name))
		}
		jobProtos = append(jobProtos, jobProto)
	}
	return &pb.ListJobSpecificationResponse{
		Jobs: jobProtos,
	}, nil
}

func (sv *RuntimeServiceServer) DumpJobSpecification(ctx context.Context, req *pb.DumpJobSpecificationRequest) (*pb.DumpJobSpecificationResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	reqJobSpec, err := sv.jobSvc.GetByName(req.GetJobName(), projSpec)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: job %s not found", err.Error(), req.GetJobName()))
	}

	compiledJob, err := sv.jobSvc.Dump(projSpec, reqJobSpec)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to compile %s", err.Error(), reqJobSpec.Name))
	}

	return &pb.DumpJobSpecificationResponse{Success: true, Content: string(compiledJob.Contents)}, nil
}

func (sv *RuntimeServiceServer) RegisterProject(ctx context.Context, req *pb.RegisterProjectRequest) (*pb.RegisterProjectResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	if err := projectRepo.Save(sv.adapter.FromProjectProto(req.GetProject())); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to save project %s", err.Error(), req.GetProject().GetName()))
	}

	return &pb.RegisterProjectResponse{
		Success: true,
		Message: "saved successfully",
	}, nil
}

func (sv *RuntimeServiceServer) ListProjects(ctx context.Context,
	req *pb.ListProjectsRequest) (*pb.ListProjectsResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projects, err := projectRepo.GetAll()
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to retrive saved projects", err.Error()))
	}

	projSpecsProto := []*pb.ProjectSpecification{}
	for _, project := range projects {
		projSpecsProto = append(projSpecsProto, sv.adapter.ToProjectProto(project))
	}

	return &pb.ListProjectsResponse{
		Projects: projSpecsProto,
	}, nil
}

func (sv *RuntimeServiceServer) RegisterInstance(ctx context.Context, req *pb.RegisterInstanceRequest) (*pb.RegisterInstanceResponse, error) {
	jobScheduledTime, err := ptypes.Timestamp(req.GetScheduledAt())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse schedule time of job %s", err.Error(), req.GetScheduledAt()))
	}

	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	jobSpec, err := sv.jobSvc.GetByName(req.GetJobName(), projSpec)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: job %s not found", err.Error(), req.GetJobName()))
	}
	jobProto, err := sv.adapter.ToJobProto(jobSpec)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: cannot adapt job %s", err.Error(), jobSpec.Name))
	}

	instance, err := sv.instSvc.Register(jobSpec, jobScheduledTime, models.InstanceType(req.Type))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to register instance of job %s", err.Error(), req.GetJobName()))
	}
	instanceProto, err := sv.adapter.ToInstanceProto(instance)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: cannot adapt instance for job %s", err.Error(), jobSpec.Name))
	}

	return &pb.RegisterInstanceResponse{
		Project:  sv.adapter.ToProjectProto(projSpec),
		Job:      jobProto,
		Instance: instanceProto,
	}, nil
}

func (sv *RuntimeServiceServer) JobStatus(ctx context.Context, req *pb.JobStatusRequest) (*pb.JobStatusResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	jobStatuses, err := sv.scheduler.GetJobStatus(ctx, projSpec, req.GetJobName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to fetch jobStatus %s", err.Error(),
			req.GetJobName()))
	}

	adaptedJobStatus := []*pb.JobStatus{}
	for _, jobStatus := range jobStatuses {
		ts, err := ptypes.TimestampProto(jobStatus.ScheduledAt)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse time for %s", err.Error(),
				req.GetJobName()))
		}
		adaptedJobStatus = append(adaptedJobStatus, &pb.JobStatus{
			State:       jobStatus.State.String(),
			ScheduledAt: ts,
		})
	}
	return &pb.JobStatusResponse{
		Statuses: adaptedJobStatus,
	}, nil
}

func (sv *RuntimeServiceServer) GetWindow(ctx context.Context, req *pb.GetWindowRequest) (*pb.GetWindowResponse, error) {
	scheduledTime, err := ptypes.Timestamp(req.GetScheduledAt())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse schedule time %s", err.Error(), req.GetScheduledAt()))
	}

	if req.GetSize() == "" || req.GetOffset() == "" || req.GetTruncateTo() == "" {
		return nil, status.Error(codes.FailedPrecondition, "window size, offset and truncate_to must be provided")
	}

	window, err := prepareWindow(req.GetSize(), req.GetOffset(), req.GetTruncateTo())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	windowStart, err1 := ptypes.TimestampProto(window.GetStart(scheduledTime))
	windowEnd, err2 := ptypes.TimestampProto(window.GetEnd(scheduledTime))
	if err1 != nil || err2 != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to convert timestamp %s", err.Error(), scheduledTime))
	}

	return &pb.GetWindowResponse{
		Start: windowStart,
		End:   windowEnd,
	}, nil
}

func (sv *RuntimeServiceServer) RegisterSecret(ctx context.Context, req *pb.RegisterSecretRequest) (*pb.RegisterSecretResponse, error) {
	if req.GetValue() == "" {
		return nil, status.Error(codes.Internal, "empty value for secret")
	}
	// decode base64
	base64Decoded, err := base64.StdEncoding.DecodeString(req.GetValue())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to decode base64 string", err.Error()))
	}

	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	secretRepo := sv.secretRepoFactory.New(projSpec)
	if err := secretRepo.Save(models.ProjectSecretItem{
		Name:  req.GetSecretName(),
		Value: string(base64Decoded),
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to save secret %s", err.Error(), req.GetSecretName()))
	}

	return &pb.RegisterSecretResponse{
		Success: true,
	}, nil
}

func (sv *RuntimeServiceServer) CreateResource(ctx context.Context, req *pb.CreateResourceRequest) (*pb.CreateResourceResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	optResource, err := sv.adapter.FromResourceProto(req.Resource)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse resource %s", err.Error(), req.Resource.GetName()))
	}

	if err := sv.resourceSvc.CreateResource(ctx, projSpec, []models.ResourceSpec{optResource}, sv.progressObserver); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to create resource %s", err.Error(), req.Resource.GetName()))
	}
	return &pb.CreateResourceResponse{
		Success: true,
	}, nil
}

func (sv *RuntimeServiceServer) UpdateResource(ctx context.Context, req *pb.UpdateResourceRequest) (*pb.UpdateResourceResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	optResource, err := sv.adapter.FromResourceProto(req.Resource)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse resource %s", err.Error(), req.Resource.GetName()))
	}

	if err := sv.resourceSvc.UpdateResource(ctx, projSpec, []models.ResourceSpec{optResource}, sv.progressObserver); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to create resource %s", err.Error(), req.Resource.GetName()))
	}
	return &pb.UpdateResourceResponse{
		Success: true,
	}, nil
}

func (sv *RuntimeServiceServer) ReadResource(ctx context.Context, req *pb.ReadResourceRequest) (*pb.ReadResourceResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	response, err := sv.resourceSvc.ReadResource(ctx, projSpec, req.DatastoreName, req.ResourceName)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to create resource %s", err.Error(), req.ResourceName))
	}

	protoResource, err := sv.adapter.ToResourceProto(response)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to adapt resource %s", err.Error(), req.ResourceName))
	}

	return &pb.ReadResourceResponse{
		Success:  true,
		Resource: protoResource,
	}, nil
}

func (sv *RuntimeServiceServer) DeployResourceSpecification(req *pb.DeployResourceSpecificationRequest, respStream pb.RuntimeService_DeployResourceSpecificationServer) error {
	startTime := time.Now()

	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	var resourceSpecs []models.ResourceSpec
	for _, resourceProto := range req.GetResources() {
		adapted, err := sv.adapter.FromResourceProto(resourceProto)
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("%s: cannot adapt resource %s", err.Error(), resourceProto.GetName()))
		}
		resourceSpecs = append(resourceSpecs, adapted)
	}

	observers := new(progress.ObserverChain)
	observers.Join(sv.progressObserver)
	observers.Join(&resourceObserver{
		stream: respStream,
		log:    logrus.New(),
	})

	if err := sv.resourceSvc.UpdateResource(respStream.Context(), projSpec, resourceSpecs, observers); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to update resources:\n%s", err.Error()))
	}
	logger.I("finished resource deployment in", time.Since(startTime))
	return nil
}

func (sv *RuntimeServiceServer) ListResourceSpecification(ctx context.Context, req *pb.ListResourceSpecificationRequest) (*pb.ListResourceSpecificationResponse, error) {
	projectRepo := sv.projectRepoFactory.New()
	projSpec, err := projectRepo.GetByName(req.GetProjectName())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: project %s not found", err.Error(), req.GetProjectName()))
	}

	resourceSpecs, err := sv.resourceSvc.GetAll(projSpec, req.DatastoreName)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to retrive jobs for project %s", err.Error(), req.GetProjectName()))
	}

	resourceProtos := []*pb.ResourceSpecification{}
	for _, resourceSpec := range resourceSpecs {
		resourceProto, err := sv.adapter.ToResourceProto(resourceSpec)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("%s: failed to parse job spec %s", err.Error(), resourceSpec.Name))
		}
		resourceProtos = append(resourceProtos, resourceProto)
	}
	return &pb.ListResourceSpecificationResponse{
		Resources: resourceProtos,
	}, nil
}

func NewRuntimeServiceServer(
	version string,
	jobSvc models.JobService,
	datastoreSvc models.DatastoreService,
	projectRepoFactory ProjectRepoFactory,
	secretRepoFactory SecretRepoFactory,
	adapter ProtoAdapter,
	progressObserver progress.Observer,
	instSvc models.InstanceService,
	scheduler models.SchedulerUnit,
) *RuntimeServiceServer {
	return &RuntimeServiceServer{
		version:            version,
		jobSvc:             jobSvc,
		resourceSvc:        datastoreSvc,
		adapter:            adapter,
		projectRepoFactory: projectRepoFactory,
		progressObserver:   progressObserver,
		instSvc:            instSvc,
		scheduler:          scheduler,
		secretRepoFactory:  secretRepoFactory,
	}
}

type jobSyncObserver struct {
	stream pb.RuntimeService_DeployJobSpecificationServer
	log    logrus.FieldLogger
}

func (obs *jobSyncObserver) Notify(e progress.Event) {
	switch evt := e.(type) {
	case *job.EventJobUpload:
		resp := &pb.DeployJobSpecificationResponse{
			Success: true,
			Ack:     true,
			JobName: evt.Job.Name,
		}
		if evt.Err != nil {
			resp.Success = false
			resp.Message = evt.Err.Error()
		}

		if err := obs.stream.Send(resp); err != nil {
			obs.log.Error(errors.Wrapf(err, "failed to send deploy spec ack for: %s", evt.Job.Name))
		}
	case *job.EventJobRemoteDelete:
		resp := &pb.DeployJobSpecificationResponse{
			JobName: evt.Name,
			Message: evt.String(),
		}
		if err := obs.stream.Send(resp); err != nil {
			obs.log.Error(errors.Wrapf(err, "failed to send delete notification for: %s", evt.Name))
		}
	case *job.EventJobSpecUnknownDependencyUsed:
		resp := &pb.DeployJobSpecificationResponse{
			JobName: evt.Job,
			Message: evt.String(),
		}
		if err := obs.stream.Send(resp); err != nil {
			obs.log.Error(errors.Wrapf(err, "failed to send unknown dependency notification for: %s", evt.Job))
		}
	}
}

type resourceObserver struct {
	stream pb.RuntimeService_DeployResourceSpecificationServer
	log    logrus.FieldLogger
}

func (obs *resourceObserver) Notify(e progress.Event) {
	switch evt := e.(type) {
	case *datastore.EventResourceUpdated:
		resp := &pb.DeployResourceSpecificationResponse{
			Success:      true,
			Ack:          true,
			ResourceName: evt.Spec.Name,
		}
		if evt.Err != nil {
			resp.Success = false
			resp.Message = evt.Err.Error()
		}

		if err := obs.stream.Send(resp); err != nil {
			obs.log.Error(errors.Wrapf(err, "failed to send deploy spec ack for: %s", evt.Spec.Name))
		}
	}
}
