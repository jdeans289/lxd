package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// urlInstanceTypeDetect detects what sort of instance type filter is being requested. Either
// explicitly via the instance-type query param or implicitly via the endpoint URL used.
func urlInstanceTypeDetect(r *http.Request) (instancetype.Type, error) {
	reqInstanceType := r.URL.Query().Get("instance-type")
	if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
		return instancetype.Container, nil
	} else if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "vm") {
		return instancetype.VM, nil
	} else if reqInstanceType != "" {
		instanceType, err := instancetype.New(reqInstanceType)
		if err != nil {
			return instancetype.Any, err
		}
		return instanceType, nil
	}

	return instancetype.Any, nil
}

// func doFilter(fstr string, container *api.InstanceFull) bool {
// 	return applyFilter(fstr, &(container.Instance))
// }

func doFilter (fstr string, result map[string][]string, d *Daemon) map[string][]string {
	// if matches filter, return true
	logger.Warnf("JackieError: %s", result)
	newResult := map[string][]string{}
	for address, containers := range result {
		// if address == "" {
		// 	address = "default"
		// }

		newContainers := []string{}
		logger.Warnf("JackieError: %s", address)
		for _,container := range containers {
			logger.Warnf("\tJackieError: %s", container)
			inst, err := instanceLoadByProjectAndName(d.State(), address, container)
			// logger.Warnf("\tJackieError: %s", inst.Config())

			if err != nil {
				continue
			}

			if applyFilter(fstr, inst) {
				newContainers = append(newContainers, container)
			}
		}

		newResult[address] = newContainers
	}

	return newResult
}

func applyFilter (fstr string, container instance.Instance) bool {
	filterSplit := strings.Fields(fstr)

	index := 0
	result := true
	prevLogical := "and"

	queryLen := len(filterSplit)

	for index < queryLen {
		field := filterSplit[index]
		operator := filterSplit[index+1]
		value := filterSplit[index+2]
		index+=3

		// eval 
		logger.Warnf("JackieError: evaluating %s %s %s", field, operator, value)

		curResult := evaluateField(field, value, operator, container);

		logger.Warnf("JackieError: %s", prevLogical)
		if prevLogical == "and" {
			logger.Warnf("JackieError: Logical AND")
			result = curResult && result
		} else {
			logger.Warnf("JackieError: Logical OR")
			result = curResult || result
		}

		if index < queryLen {
			prevLogical = filterSplit[index]
			index++
		}
	}

	return result
}

func evaluateField (field string, value string, op string, container instance.Instance) bool {
	result := false
	// logger.Warnf("JackieError %q", container.ExpandedConfig())
	switch {
		case field == "name":
			result = value == container.Name()
			break

		case strings.HasPrefix(field, "config"):
			fieldCut := field[7:len(field)]
			logger.Warnf("Field chopped: %s", fieldCut)
			config := container.ExpandedConfig()
			result = config[fieldCut] == value
			break

		default:
			result = false
	}

	if op == "neq" {
		result = !result
	}

	return result
}


func containersGet(d *Daemon, r *http.Request) response.Response {
	for i := 0; i < 100; i++ {
		result, err := doContainersGet(d, r)
		if err == nil {
			// filterStr := r.FormValue("filter")
			// if filterStr != "" {
			// result = doFilter(filterStr, result)
			// }
			return response.SyncResponse(true, result)
		}
		if !query.IsRetriableError(err) {
			logger.Debugf("DBERR: containersGet: error %q", err)
			return response.SmartError(err)
		}
		// 100 ms may seem drastic, but we really don't want to thrash
		// perhaps we should use a random amount
		time.Sleep(100 * time.Millisecond)
	}

	logger.Debugf("DBERR: containersGet, db is locked")
	logger.Debugf(logger.GetStack())
	return response.InternalError(fmt.Errorf("DB is locked"))
}

func doContainersGet(d *Daemon, r *http.Request) (interface{}, error) {
	resultString := []string{}
	resultList := []*api.Instance{}
	resultFullList := []*api.InstanceFull{}
	resultMu := sync.Mutex{}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return nil, err
	}

	// Parse the recursion field
	recursionStr := r.FormValue("recursion")

	// // Parse filter value
	filterStr := r.FormValue("filter")

	if filterStr != "" {
		logger.Warnf("JackieError: %s", filterStr)
	}

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	// Parse the project field
	project := projectParam(r)
	// logger.Warnf("JackieError: Project param: %s", project)


	inst, err := instanceLoadByProjectAndName(d.State(), project, "first")
	// doFilter(filterStr, inst)
	// c,_,_ := inst.Render()
	// c.ExtendedConfig
	logger.Warnf("GOT INSTANCE: %d", inst.ID)

	// Get the list and location of all containers
	var result map[string][]string // Containers by node address
	var nodes map[string]string    // Node names by container
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		result, err = tx.ContainersListByNodeAddress(project, instanceType)
		if err != nil {
			return err
		}

		nodes, err = tx.ContainersByNodeName(project, instanceType)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return []string{}, err
	}
	logger.Warnf("JackieError: RESULT %s", result)
	// Get the local instances
	nodeCts := map[string]instance.Instance{}
	if recursion > 0 {
		cts, err := instanceLoadNodeProjectAll(d.State(), project, instanceType)
		if err != nil {
			return nil, err
		}

		for _, ct := range cts {
			nodeCts[ct.Name()] = ct
		}
	}

	// Append containers to list and handle errors
	resultListAppend := func(name string, c api.Instance, err error) {
		if err != nil {
			c = api.Instance{
				Name:       name,
				Status:     api.Error.String(),
				StatusCode: api.Error,
				Location:   nodes[name],
			}
		}
		// doFilter("filterStr", c)
		logger.Warnf("JackieError: CHECK 1 - c = %s", c)
		resultMu.Lock()
		resultList = append(resultList, &c)
		resultMu.Unlock()
	}

	resultFullListAppend := func(name string, c api.InstanceFull, err error) {
		if err != nil {
			c = api.InstanceFull{Instance: api.Instance{
				Name:       name,
				Status:     api.Error.String(),
				StatusCode: api.Error,
				Location:   nodes[name],
			}}
		}
		// doFilter("filterStr", c)
		logger.Warnf("JackieError: CHECK 2")
		resultMu.Lock()
		resultFullList = append(resultFullList, &c)
		resultMu.Unlock()
	}

	// TIME TO FILTER
	logger.Warnf("JackieError: RESULT2 %s", result)
	if filterStr != "" {
		result = doFilter(filterStr, result, d)
	}	

	logger.Warnf("JackieError: result after filter %s", result)

	// Get the data
	wg := sync.WaitGroup{}
	for address, containers := range result {
		// If this is an internal request from another cluster node,
		// ignore containers from other nodes, and return only the ones
		// on this node
		if isClusterNotification(r) && address != "" {
			continue
		}

		// Mark containers on unavailable nodes as down
		if recursion > 0 && address == "0.0.0.0" {
			for _, container := range containers {
				if recursion == 1 {
					resultListAppend(container, api.Instance{}, fmt.Errorf("unavailable"))
				} else {
					resultFullListAppend(container, api.InstanceFull{}, fmt.Errorf("unavailable"))
				}
			}

			continue
		}

		// For recursion requests we need to fetch the state of remote
		// containers from their respective nodes.
		if recursion > 0 && address != "" && !isClusterNotification(r) {
			wg.Add(1)
			go func(address string, containers []string) {
				defer wg.Done()
				cert := d.endpoints.NetworkCert()

				if recursion == 1 {
					cs, err := doContainersGetFromNode(project, address, cert, instanceType)
					if err != nil {
						for _, name := range containers {
							resultListAppend(name, api.Instance{}, err)
						}

						return
					}

					for _, c := range cs {
						resultListAppend(c.Name, c, nil)
					}

					return
				}

				cs, err := doContainersFullGetFromNode(project, address, cert, instanceType)
				if err != nil {
					for _, name := range containers {
						resultFullListAppend(name, api.InstanceFull{}, err)
					}

					return
				}

				for _, c := range cs {
					resultFullListAppend(c.Name, c, nil)
				}
			}(address, containers)

			continue
		}

		if recursion == 0 {
			for _, container := range containers {
				instancePath := "instances"
				if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "container") {
					instancePath = "containers"
				} else if strings.HasPrefix(mux.CurrentRoute(r).GetName(), "vm") {
					instancePath = "virtual-machines"
				}
				url := fmt.Sprintf("/%s/%s/%s", version.APIVersion, instancePath, container)
				logger.Warnf("JackieError: CHECK 3")
				resultString = append(resultString, url)
			}
		} else {
			threads := 4
			if len(containers) < threads {
				threads = len(containers)
			}

			queue := make(chan string, threads)

			for i := 0; i < threads; i++ {
				wg.Add(1)

				go func() {
					for {
						container, more := <-queue
						if !more {
							break
						}

						if recursion == 1 {
							c, _, err := nodeCts[container].Render()
							if err != nil {
								resultListAppend(container, api.Instance{}, err)
							} else {
								resultListAppend(container, *c.(*api.Instance), err)
							}

							continue
						}

						c, _, err := nodeCts[container].RenderFull()
						if err != nil {
							resultFullListAppend(container, api.InstanceFull{}, err)
						} else {
							resultFullListAppend(container, *c, err)
						}
					}

					wg.Done()
				}()
			}

			for _, container := range containers {
				queue <- container
			}

			close(queue)
		}
	}
	wg.Wait()

	// if recursion == 2 {
	// 	return []string{"Hello!!!!!!!"}, nil
	// }

	if recursion == 0 {
		logger.Warnf("JackieError: CHECK 4")
		logger.Warnf("JackieError: Result String %s", resultList)
		return resultString, nil
	}

	if recursion == 1 {
		// Sort the result list by name.
		sort.Slice(resultList, func(i, j int) bool {
			return resultList[i].Name < resultList[j].Name
		})

		// logger.Warnf("JackieError: Result List %s", resultList)
		return resultList, nil
	}

	// Sort the result list by name.
	sort.Slice(resultFullList, func(i, j int) bool {
		return resultFullList[i].Name < resultFullList[j].Name
	})

	logger.Warnf("JackieError: Result Full List %s", resultList)
	return resultFullList, nil
}

// Fetch information about the containers on the given remote node, using the
// rest API and with a timeout of 30 seconds.
func doContainersGetFromNode(project, node string, cert *shared.CertInfo, instanceType instancetype.Type) ([]api.Instance, error) {
	f := func() ([]api.Instance, error) {
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		client = client.UseProject(project)

		containers, err := client.GetInstances(api.InstanceType(instanceType.String()))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
		}

		return containers, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var containers []api.Instance
	var err error

	go func() {
		containers, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting instances from node %s", node)
	case <-done:
	}

	return containers, err
}

func doContainersFullGetFromNode(project, node string, cert *shared.CertInfo, instanceType instancetype.Type) ([]api.InstanceFull, error) {
	f := func() ([]api.InstanceFull, error) {
		client, err := cluster.Connect(node, cert, true)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to connect to node %s", node)
		}

		client = client.UseProject(project)

		instances, err := client.GetInstancesFull(api.InstanceType(instanceType.String()))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to get instances from node %s", node)
		}

		return instances, nil
	}

	timeout := time.After(30 * time.Second)
	done := make(chan struct{})

	var instances []api.InstanceFull
	var err error

	go func() {
		instances, err = f()
		done <- struct{}{}
	}()

	select {
	case <-timeout:
		err = fmt.Errorf("Timeout getting instances from node %s", node)
	case <-done:
	}

	return instances, err
}
