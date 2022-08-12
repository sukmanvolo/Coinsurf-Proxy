package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cockroachdb/errors"
	"github.com/coinsurf-com/proxy/internal/zerocopy"
	"github.com/coinsurf-com/proxy/pkg"
	"github.com/go-redis/redis/v8"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/panjf2000/ants/v2"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
	"github.com/valyala/fasttemplate"
	"go.uber.org/zap"
	"strings"
	"sync"
	"time"
)

type Task struct {
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Success struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	} `json:"success"`
	MaxTries            int      `json:"max_tries"`
	ApplicableCountries []string `json:"applicable_countries"`
	Schedule            int      `json:"schedule"`
}

type Redis struct {
	mu                 sync.RWMutex
	tasks              map[string]*Task
	channels           map[string]chan struct{}
	pattern            string
	pool               *ants.Pool
	rd                 *redis.Client
	router             pkg.Router
	dialerTimeout      time.Duration
	requestTimeout     time.Duration
	fetchTasksInterval time.Duration
	initialDelay       time.Duration
	usernameTemplate   *fasttemplate.Template
	logger             *zap.Logger
}

func NewRedis(
	tasksDB string,
	fetchTasksInterval, dialerTimeout, requestTimeout, initialDelay time.Duration,
	poolCapacity int,
	ip string,
	userPassword string,
	r *redis.Client,
	router pkg.Router,
	logger *zap.Logger) (*Redis, error) {

	p, err := ants.NewPool(poolCapacity)
	if err != nil {
		return nil, err
	}

	pattern := fmt.Sprintf(":%s:*", tasksDB)

	t, err := fasttemplate.NewTemplate(fmt.Sprintf("ip-{{ip}}:%s@%s", userPassword, ip), "{{", "}}")
	if err != nil {
		return nil, err
	}

	rd := &Redis{
		mu:                 sync.RWMutex{},
		pool:               p,
		usernameTemplate:   t,
		rd:                 r,
		router:             router,
		dialerTimeout:      dialerTimeout,
		requestTimeout:     requestTimeout,
		initialDelay:       initialDelay,
		fetchTasksInterval: fetchTasksInterval,
		pattern:            pattern,
		logger:             logger}

	return rd, nil
}

func (r *Redis) Serve(ctx context.Context) error {
	var fetchTasks = func(pattern string) (map[string]*Task, []string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()

		keys, err := r.rd.Keys(ctx, pattern).Result()
		if err != nil {
			return nil, nil, err
		}

		names := make([]string, 0)
		tasks := make(map[string]*Task, 0)
		for _, key := range keys {
			result, err := r.rd.Get(context.Background(), key).Result()
			if err != nil {
				return nil, nil, err
			}

			var task = new(Task)
			err = json.Unmarshal(zerocopy.Bytes(result), task)
			if err != nil {
				return nil, nil, errors.Wrap(err, "invalid task format, key: "+key)
			}

			tasks[task.Name] = task
			names = append(names, task.Name)
		}

		return tasks, names, nil
	}

	var err error
	r.tasks, _, err = fetchTasks(r.pattern)
	if err != nil {
		return err
	}

	r.channels = make(map[string]chan struct{})
	//initial task scheduling
	go func() {
		time.Sleep(r.initialDelay)
		r.mu.Lock()
		for _, task := range r.tasks {
			ch := make(chan struct{})
			r.channels[task.Name] = ch
			go r.scheduleTask(ch, task)
		}
		r.mu.Unlock()
	}()

	//Fetch tasks from Redis
	go func() {
		ticker := time.NewTicker(r.fetchTasksInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				all, names, err := fetchTasks(r.pattern)
				if err != nil {
					r.logger.Error("failed to fetch task list", zap.Error(err))
					continue
				}

				err = r.router.PoolCleanNameNotIn(names...)
				if err != nil {
					r.logger.Error("failed to clean pool where name not in",
						zap.Error(err),
						zap.Strings("names", names))
					continue
				}

				r.mu.Lock()
				//find new tasks and add channels to the map
				for _, t := range all {
					if _, ok := r.tasks[t.Name]; !ok {
						r.tasks[t.Name] = t
					}
				}

				//find removed tasks and close their channels
				for name := range r.tasks {
					if _, ok := all[name]; !ok {
						ch, ok := r.channels[name]
						if !ok {
							continue
						}

						ch <- struct{}{}
						close(ch)
						delete(r.channels, name)
						delete(r.tasks, name)
					}
				}

				//schedule new tasks
				for _, task := range r.tasks {
					if _, ok := r.channels[task.Name]; ok {
						continue
					}

					ch := make(chan struct{})
					r.channels[task.Name] = ch
					go r.scheduleTask(ch, task)
				}
				r.mu.Unlock()
			}
		}
	}()

	return nil
}

func (r *Redis) scheduleTask(ch chan struct{}, task *Task) {
	ticker := time.NewTicker(time.Duration(task.Schedule) * time.Minute)
	defer ticker.Stop()

	r.logger.Debug(fmt.Sprintf("Started task %s", task.Name))
	defer r.logger.Debug(fmt.Sprintf("Stopped task %s", task.Name))

	var run = func() {
		nodes, err := r.router.GetAll(pkg.FilterCountry(task.ApplicableCountries...))
		if err != nil {
			r.logger.Error("failed to get nodes",
				zap.Strings("countries", task.ApplicableCountries),
				zap.Error(err))
			return
		}

		if len(nodes) == 0 {
			return
		}

		var wg = sync.WaitGroup{}
		wg.Add(len(nodes))

		var mu = sync.Mutex{}
		var success = make([]peer.ID, 0)
		var failed = make([]peer.ID, 0)

		for _, n := range nodes {
			//TODO use ants pool here to reduce number of concurrent goroutines
			//err := r.pool.Submit(func() {
			go func(node *pkg.Node) {
				defer wg.Done()

				proxy := r.usernameTemplate.ExecuteString(map[string]interface{}{
					"ip": node.IP(),
				})

				c := &fasthttp.Client{
					Dial:                          fasthttpproxy.FasthttpHTTPDialerTimeout(proxy, r.dialerTimeout),
					DisablePathNormalizing:        true,
					DisableHeaderNamesNormalizing: true,
					NoDefaultUserAgentHeader:      true,
				}

				request := fasthttp.AcquireRequest()
				defer fasthttp.ReleaseRequest(request)

				response := fasthttp.AcquireResponse()
				defer fasthttp.ReleaseResponse(response)

				for name, value := range task.Headers {
					if name == fasthttp.HeaderConnection && strings.EqualFold(value, "close") {
						request.SetConnectionClose()
					}

					request.Header.Set(name, value)
				}
				request.SetRequestURI(task.URL)
				request.Header.SetMethod(strings.ToUpper(task.Method))
				var tries int
				c.RetryIf = func(request *fasthttp.Request) bool {
					tries++
					return tries < task.MaxTries
				}

				err = c.DoTimeout(request, response, r.requestTimeout)
				if err != nil {
					r.logger.Warn("failed to execute request",
						zap.String("ip", node.IP()), zap.Error(err))
					mu.Lock()
					failed = append(failed, node.ID)
					mu.Unlock()
					return
				}

				if response.StatusCode() != task.Success.StatusCode {
					mu.Lock()
					failed = append(failed, node.ID)
					mu.Unlock()
					return
				}

				if task.Success.Body != "" && zerocopy.String(response.Body()) != task.Success.Body {
					mu.Lock()
					failed = append(failed, node.ID)
					mu.Unlock()
					return
				}

				mu.Lock()
				success = append(success, node.ID)
				mu.Unlock()
			}(n)
			//if err != nil {
			//	r.logger.Error("failed to submit node check task",
			//		zap.String("ip", nonde.IP()),
			//		zap.Error(err))
			//	return
			//}
		}
		wg.Wait()

		if len(success) > 0 {
			err = r.router.PoolAdd(task.Name, success...)
			if err != nil {
				r.logger.Error("failed to add nodes to pool", zap.Error(err))
			}
		}

		if len(failed) > 0 {
			err = r.router.PoolDelete(task.Name, failed...)
			if err != nil {
				r.logger.Error("failed to delete nodes from pool", zap.Error(err))
			}
		}

	}

	run()

	for {
		select {
		case <-ch:
			return
		case <-ticker.C:
			run()
		}
	}
}
