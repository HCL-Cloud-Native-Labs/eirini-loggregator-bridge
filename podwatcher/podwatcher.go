package podwatcher

import (
	"fmt"

	config "github.com/SUSE/eirini-loggregator-bridge/config"
	. "github.com/SUSE/eirini-loggregator-bridge/logger"
	eirinix "github.com/SUSE/eirinix"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type PodWatcher struct {
	Config     config.ConfigType
	Containers ContainerList
	Manager    eirinix.Manager
}

type Container struct {
	killChannel   chan bool
	PodName       string
	Namespace     string
	Name          string
	PodUID        string
	UID           string
	InitContainer bool
	ContainerList *ContainerList
}

type ContainerList struct {
	PodWatcher *PodWatcher
	Containers map[string]*Container
}

func (cl *ContainerList) GetContainer(uid string) (*Container, bool) {
	c, ok := cl.Containers[uid]
	return c, ok
}

func (cl *ContainerList) AddContainer(c *Container) {
	c.ContainerList = cl
	cl.Containers[c.UID] = c
}

func (cl *ContainerList) RemoveContainer(uid string) error {
	_, ok := cl.GetContainer(uid)
	if !ok {
		return errors.New("No container found")
	}
	// TODO: Cleanup goroutine with killChannel
	delete(cl.Containers, uid)
	return nil
}

// EnsureContainer make sure the container exists in the list and we are
// monitoring it.
func (cl ContainerList) EnsureContainer(c *Container) error {
	// TODO: implement this
	LogDebug(c.UID + ": ensuring container is monitored")

	if _, ok := cl.GetContainer(c.UID); !ok {
		cl.AddContainer(c)
	}
	return nil
}

func (cl ContainerList) RemovePODContainers(podUID string) error {
	// TODO: Fix this, and remove all containers belonging to a POD
	LogDebug("Removing POD's containers ", podUID)

	return nil
}

// Tail connects to the Kube
func (c Container) Tail() error {
	return nil
}

func generateContainerUID(pod *corev1.Pod, container corev1.Container) string {
	return fmt.Sprintf("%s-%s", string(pod.UID), container.Name)
}

func findContainerState(name string, containerStatuses []corev1.ContainerStatus) *corev1.ContainerState {
	for _, status := range containerStatuses {
		if status.Name == name {
			return &status.State
		}
	}
	return nil
}

// EnsurePodStatus handles a pod event by adding or removing container tailing
// goroutines. Every running container in the monitored namespace has its own
// goroutine that reads its log stream. When a container is stopped we stop
// the relevant gorouting (if it is still running, it could already be stopped
// because of an error).
func (cl ContainerList) EnsurePodStatus(pod *corev1.Pod) error {
	// Lookup maps for event containers, to make comparison with our known
	// state faster.
	podInitContainers := map[string]string{}
	podContainers := map[string]string{}

	for _, c := range pod.Spec.InitContainers {
		cUID := generateContainerUID(pod, c)
		podInitContainers[cUID] = cUID
		cState := findContainerState(c.Name, pod.Status.InitContainerStatuses)
		//		LogDebug("status:", pod.Status.InitContainerStatuses[k].State)
		if cState != nil && cState.Running != nil {
			cl.EnsureContainer(&Container{Name: c.Name,
				PodName:       pod.Name,
				PodUID:        string(pod.UID),
				UID:           cUID,
				Namespace:     pod.Namespace,
				killChannel:   make(chan bool),
				InitContainer: true,
			})
		} else {
			err := cl.RemoveContainer(cUID)
			if err != nil {
				return err
			}
			LogDebug(cUID + " init container is not running, ensure we are NOT streaming")
		}

	}

	for _, c := range pod.Spec.Containers {
		cUID := generateContainerUID(pod, c)
		podContainers[cUID] = cUID
		cState := findContainerState(c.Name, pod.Status.ContainerStatuses)
		if cState != nil && cState.Running != nil {
			cl.EnsureContainer(&Container{Name: c.Name,
				PodName:       pod.Name,
				PodUID:        string(pod.UID),
				UID:           cUID,
				Namespace:     pod.Namespace,
				killChannel:   make(chan bool),
				InitContainer: false,
			})
		} else {
			err := cl.RemoveContainer(cUID)
			if err != nil {
				return err
			}
			LogDebug(cUID + " container is not running, ensure we are NOT streaming")
		}

	}

	// Cleanup left-over containers from our list
	for _, c := range cl.Containers {
		if c.InitContainer {
			if _, ok := podInitContainers[c.UID]; !ok {
				cl.RemoveContainer(c.UID)
			}
		} else {
			if _, ok := podContainers[c.UID]; !ok {
				cl.RemoveContainer(c.UID)
			}
		}
	}

	return nil
}

func NewPodWatcher(config config.ConfigType, manager eirinix.Manager) eirinix.Watcher {
	pw := &PodWatcher{
		Config:  config,
		Manager: manager}
	// We need a way to go up the hierarchy (e.g. to access the Manager from the Container):
	// Manager -> PodWatcher -> ContainerList -> Container
	pw.Containers = ContainerList{PodWatcher: pw, Containers: map[string]*Container{}}

	return pw
}

func (pw *PodWatcher) Handle(manager eirinix.Manager, e watch.Event) {
	manager.GetLogger().Debug("Received event: ", e)
	if e.Object == nil {
		// Closed because of error
		// TODO: Handle errors ( maybe kill the whole application )
		// because it is going to run in goroutines, and we can't
		// just return gracefully and panicking the whole
		return
	}

	pod, ok := e.Object.(*corev1.Pod)
	if !ok {
		manager.GetLogger().Error("Received non-pod object in watcher channel")
		return
	}

	pw.Containers.EnsurePodStatus(pod)

	// TODO:
	// - Consume a kubeclient ✓ -> moved to eirinix
	// - Create kube watcher ✓ -> moved to eirinix
	// - Select on channels and handle events and spin up go routines for the new pod
	//   Or stop goroutine for removed pods
	//   Those goroutines read the logs  of the pod from the kube api and simply  writes metadata to a channel.
	// - Then we have one or more reader instances that consumes the channel, converting metadata to loggregator envelopes and streams that to loggregator
}
