package job_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	testMock "github.com/stretchr/testify/mock"
	"github.com/odpf/optimus/job"
	"github.com/odpf/optimus/mock"
	"github.com/odpf/optimus/models"
)

func TestService(t *testing.T) {
	ctx := context.Background()
	t.Run("Create", func(t *testing.T) {
		t.Run("should create a new JobSpec and store in repository", func(t *testing.T) {
			jobSpec := models.JobSpec{
				Version: 1,
				Name:    "test",
				Owner:   "optimus",
				Schedule: models.JobSpecSchedule{
					StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
					Interval:  "@daily",
				},
			}
			projSpec := models.ProjectSpec{
				Name: "proj",
			}

			repo := new(mock.JobSpecRepository)
			repo.On("Save", jobSpec).Return(nil)
			defer repo.AssertExpectations(t)

			repoFac := new(mock.JobSpecRepoFactory)
			repoFac.On("New", projSpec).Return(repo)
			defer repoFac.AssertExpectations(t)

			svc := job.NewService(repoFac, nil, nil, nil, nil, nil)
			err := svc.Create(jobSpec, projSpec)
			assert.Nil(t, err)
		})
		t.Run("should fail if saving to repo fails", func(t *testing.T) {
			jobSpec := models.JobSpec{
				Version: 1,
				Name:    "test",
				Owner:   "optimus",
				Schedule: models.JobSpecSchedule{
					StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
					Interval:  "@daily",
				},
			}
			projSpec := models.ProjectSpec{
				Name: "proj",
			}

			repo := new(mock.JobSpecRepository)
			repo.On("Save", jobSpec).Return(errors.New("unknown error"))
			defer repo.AssertExpectations(t)

			repoFac := new(mock.JobSpecRepoFactory)
			repoFac.On("New", projSpec).Return(repo)
			defer repoFac.AssertExpectations(t)

			svc := job.NewService(repoFac, nil, nil, nil, nil, nil)
			err := svc.Create(jobSpec, projSpec)
			assert.NotNil(t, err)
		})
	})
	t.Run("Sync", func(t *testing.T) {
		projSpec := models.ProjectSpec{
			Name: "proj",
		}
		t.Run("should successfully store job specs for the requested project", func(t *testing.T) {
			jobSpecsBase := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterDepenResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterPriorityResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{
						Priority: 10000,
					},
				},
			}

			jobs := []models.Job{
				{
					Name:     "test",
					Contents: []byte(`come string`),
				},
			}

			// used to store raw job specs
			jobSpecRepo := new(mock.JobSpecRepository)
			jobSpecRepo.On("GetAll").Return(jobSpecsBase, nil)
			defer jobSpecRepo.AssertExpectations(t)

			jobSpecRepoFac := new(mock.JobSpecRepoFactory)
			jobSpecRepoFac.On("New", projSpec).Return(jobSpecRepo)
			defer jobSpecRepoFac.AssertExpectations(t)

			// used to store compiled job specs
			jobRepo := new(mock.JobRepository)
			jobRepo.On("ListNames", ctx).Return([]string{"test"}, nil)
			defer jobRepo.AssertExpectations(t)

			jobRepoFac := new(mock.JobRepoFactory)
			jobRepoFac.On("New", context.Background(), projSpec).Return(jobRepo, nil)
			defer jobRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, jobSpecRepo, jobSpecsBase[0], nil).Return(jobSpecsAfterDepenResolve[0], nil)
			defer depenResolver.AssertExpectations(t)

			// resolve priority
			priorityResolver := new(mock.PriorityResolver)
			priorityResolver.On("Resolve", jobSpecsAfterDepenResolve).Return(jobSpecsAfterPriorityResolve, nil)
			defer priorityResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			// compile to dag and save
			for idx, compiledJob := range jobs {
				compiler.On("Compile", jobSpecsAfterPriorityResolve[idx], projSpec).Return(compiledJob, nil)
				jobRepo.On("Save", ctx, compiledJob).Return(nil)
			}

			svc := job.NewService(jobSpecRepoFac, jobRepoFac, compiler, depenResolver, priorityResolver, nil)
			err := svc.Sync(ctx, projSpec, nil)
			assert.Nil(t, err)
		})
		t.Run("should delete job specs from target store if there are existing specs that are no longer present in job specs", func(t *testing.T) {
			jobSpecsBase := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterDepenResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterPriorityResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{
						Priority: 10000,
					},
				},
			}

			jobs := []models.Job{
				{
					Name:     "test",
					Contents: []byte(`some string`),
				},
				{
					Name:     "test2",
					Contents: []byte(`some string`),
				},
			}

			// used to store raw job specs
			jobSpecRepo := new(mock.JobSpecRepository)
			defer jobSpecRepo.AssertExpectations(t)

			jobSpecRepoFac := new(mock.JobSpecRepoFactory)
			defer jobSpecRepoFac.AssertExpectations(t)

			// used to store compiled job specs
			jobRepo := new(mock.JobRepository)
			defer jobRepo.AssertExpectations(t)

			jobRepoFac := new(mock.JobRepoFactory)
			defer jobRepoFac.AssertExpectations(t)

			depenResolver := new(mock.DependencyResolver)
			defer depenResolver.AssertExpectations(t)

			priorityResolver := new(mock.PriorityResolver)
			defer priorityResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			jobSpecRepoFac.On("New", projSpec).Return(jobSpecRepo)
			jobRepo.On("ListNames", ctx).Return([]string{"test", "test2"}, nil)

			// resolve dependencies
			depenResolver.On("Resolve", projSpec, jobSpecRepo, jobSpecsBase[0], nil).Return(jobSpecsAfterDepenResolve[0], nil)

			// resolve priority
			priorityResolver.On("Resolve", jobSpecsAfterDepenResolve).Return(jobSpecsAfterPriorityResolve, nil)
			jobRepoFac.On("New", context.Background(), projSpec).Return(jobRepo, nil)

			// compile to dag and save the first one
			compiler.On("Compile", jobSpecsAfterPriorityResolve[0], projSpec).Return(jobs[0], nil)
			jobRepo.On("Save", ctx, jobs[0]).Return(nil)

			// fetch currently stored
			jobSpecRepo.On("GetAll").Return(jobSpecsBase, nil)

			// delete unwanted
			//jobSpecRepo.On("Delete", jobs[1].Name).Return(nil)
			jobRepo.On("Delete", ctx, jobs[1].Name).Return(nil)

			svc := job.NewService(jobSpecRepoFac, jobRepoFac, compiler, depenResolver, priorityResolver, nil)
			err := svc.Sync(ctx, projSpec, nil)
			assert.Nil(t, err)
		})
		t.Run("should batch dependency resolution errors if any for all jobs", func(t *testing.T) {
			jobSpecsBase := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
				{
					Version: 1,
					Name:    "test-2",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}

			// used to store raw job specs
			jobSpecRepo := new(mock.JobSpecRepository)
			jobSpecRepo.On("GetAll").Return(jobSpecsBase, nil)
			defer jobSpecRepo.AssertExpectations(t)

			jobSpecRepoFac := new(mock.JobSpecRepoFactory)
			jobSpecRepoFac.On("New", projSpec).Return(jobSpecRepo)
			defer jobSpecRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, jobSpecRepo, jobSpecsBase[0], nil).Return(models.JobSpec{}, errors.New("error test"))
			depenResolver.On("Resolve", projSpec, jobSpecRepo, jobSpecsBase[1], nil).Return(models.JobSpec{},
				errors.New("error test-2"))
			defer depenResolver.AssertExpectations(t)

			svc := job.NewService(jobSpecRepoFac, nil, nil, depenResolver, nil, nil)
			err := svc.Sync(ctx, projSpec, nil)
			assert.NotNil(t, err)
			assert.True(t, strings.Contains(err.Error(), "2 errors occurred"))
			assert.True(t, strings.Contains(err.Error(), "error test"))
			assert.True(t, strings.Contains(err.Error(), "error test-2"))
		})
		t.Run("should successfully publish metadata for all job specs", func(t *testing.T) {
			jobSpecsBase := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterDepenResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterPriorityResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{
						Priority: 10000,
					},
				},
			}

			jobs := []models.Job{
				{
					Name:     "test",
					Contents: []byte(`come string`),
				},
			}

			// used to store raw job specs
			jobSpecRepo := new(mock.JobSpecRepository)
			jobSpecRepo.On("GetAll").Return(jobSpecsBase, nil)
			defer jobSpecRepo.AssertExpectations(t)

			jobSpecRepoFac := new(mock.JobSpecRepoFactory)
			jobSpecRepoFac.On("New", projSpec).Return(jobSpecRepo)
			defer jobSpecRepoFac.AssertExpectations(t)

			// used to store compiled job specs
			jobRepo := new(mock.JobRepository)
			jobRepo.On("ListNames", ctx).Return([]string{"test"}, nil)
			defer jobRepo.AssertExpectations(t)

			jobRepoFac := new(mock.JobRepoFactory)
			jobRepoFac.On("New", testMock.Anything, projSpec).Return(jobRepo, nil)
			defer jobRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, jobSpecRepo, jobSpecsBase[0], nil).Return(jobSpecsAfterDepenResolve[0], nil)
			defer depenResolver.AssertExpectations(t)

			// resolve priority
			priorityResolver := new(mock.PriorityResolver)
			priorityResolver.On("Resolve", jobSpecsAfterDepenResolve).Return(jobSpecsAfterPriorityResolve, nil)
			defer priorityResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			metaSvc := new(mock.MetaService)
			metaSvc.On("Publish", projSpec, jobSpecsAfterPriorityResolve, nil).Return(nil)
			defer metaSvc.AssertExpectations(t)

			metaSvcFact := new(mock.MetaSvcFactory)
			metaSvcFact.On("New").Return(metaSvc)
			defer metaSvcFact.AssertExpectations(t)

			// compile to dag and save
			for idx, compiledJob := range jobs {
				compiler.On("Compile", jobSpecsAfterPriorityResolve[idx], projSpec).Return(compiledJob, nil)
				jobRepo.On("Save", ctx, compiledJob).Return(nil)
			}

			svc := job.NewService(jobSpecRepoFac, jobRepoFac, compiler, depenResolver, priorityResolver, metaSvcFact)
			err := svc.Sync(ctx, projSpec, nil)
			assert.Nil(t, err)
		})
	})
	t.Run("KeepOnly", func(t *testing.T) {
		projSpec := models.ProjectSpec{
			Name: "proj",
		}
		t.Run("should keep only provided specs and delete rest", func(t *testing.T) {
			jobSpecsBase := []models.JobSpec{
				{
					Version: 1,
					Name:    "test-1",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
				{
					Version: 1,
					Name:    "test-2",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}

			toKeep := []models.JobSpec{
				{
					Version: 1,
					Name:    "test-2",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}

			// used to store raw job specs
			jobSpecRepo := new(mock.JobSpecRepository)
			defer jobSpecRepo.AssertExpectations(t)

			jobSpecRepoFac := new(mock.JobSpecRepoFactory)
			defer jobSpecRepoFac.AssertExpectations(t)

			jobSpecRepoFac.On("New", projSpec).Return(jobSpecRepo)
			// fetch currently stored
			jobSpecRepo.On("GetAll").Return(jobSpecsBase, nil)
			// delete unwanted
			jobSpecRepo.On("Delete", jobSpecsBase[0].Name).Return(nil)

			svc := job.NewService(jobSpecRepoFac, nil, nil, nil, nil, nil)
			err := svc.KeepOnly(projSpec, toKeep, nil)
			assert.Nil(t, err)
		})
	})
	t.Run("Dump", func(t *testing.T) {
		projSpec := models.ProjectSpec{
			Name: "proj",
		}
		t.Run("should successfully generate compiled job", func(t *testing.T) {
			jobSpecsBase := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterDepenResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{},
				},
			}
			jobSpecsAfterPriorityResolve := []models.JobSpec{
				{
					Version: 1,
					Name:    "test",
					Owner:   "optimus",
					Schedule: models.JobSpecSchedule{
						StartDate: time.Date(2020, 12, 02, 0, 0, 0, 0, time.UTC),
						Interval:  "@daily",
					},
					Task: models.JobSpecTask{
						Priority: 10000,
					},
				},
			}

			jobs := []models.Job{
				{
					Name:     "test",
					Contents: []byte(`come string`),
				},
			}

			// used to store raw job specs
			jobSpecRepo := new(mock.JobSpecRepository)
			jobSpecRepo.On("GetAll").Return(jobSpecsBase, nil)
			defer jobSpecRepo.AssertExpectations(t)

			jobSpecRepoFac := new(mock.JobSpecRepoFactory)
			jobSpecRepoFac.On("New", projSpec).Return(jobSpecRepo)
			defer jobSpecRepoFac.AssertExpectations(t)

			// used to store compiled job specs
			jobRepo := new(mock.JobRepository)
			defer jobRepo.AssertExpectations(t)

			jobRepoFac := new(mock.JobRepoFactory)
			defer jobRepoFac.AssertExpectations(t)

			// resolve dependencies
			depenResolver := new(mock.DependencyResolver)
			depenResolver.On("Resolve", projSpec, jobSpecRepo, jobSpecsBase[0], nil).Return(jobSpecsAfterDepenResolve[0], nil)
			defer depenResolver.AssertExpectations(t)

			// resolve priority
			priorityResolver := new(mock.PriorityResolver)
			priorityResolver.On("Resolve", jobSpecsAfterDepenResolve).Return(jobSpecsAfterPriorityResolve, nil)
			defer priorityResolver.AssertExpectations(t)

			compiler := new(mock.Compiler)
			defer compiler.AssertExpectations(t)

			// compile to dag and save
			for idx, compiledJob := range jobs {
				compiler.On("Compile", jobSpecsAfterPriorityResolve[idx], projSpec).Return(compiledJob, nil)
			}

			svc := job.NewService(jobSpecRepoFac, jobRepoFac, compiler, depenResolver, priorityResolver, nil)
			compiledJob, err := svc.Dump(projSpec, jobSpecsBase[0])
			assert.Nil(t, err)
			assert.Equal(t, "come string", string(compiledJob.Contents))
			assert.Equal(t, "test", compiledJob.Name)
		})
	})
}
