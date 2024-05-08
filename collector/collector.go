package collector

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

/**
 * @function: 定义
 * @desc:
 * @return {*}
 */
type Metrics struct {
	metrics    map[string]*prometheus.Desc
	mutex      sync.Mutex
	clientset  *kubernetes.Clientset
	httpClient *http.Client
}

/*
*

  - @function: 封装NewDesc
  - @param： metricName 指标名称
  - @param: docString  指标帮助信息
  - @param: labels   标签信息
  - @return
*/
func newGlobalMetric(metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(metricName, docString, labels, nil)
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

// 初始化Metrics 结构体信息
func NewMetrics() *Metrics {
	var config *rest.Config
	var err error
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" && os.Getenv("KUBERNETES_SERVICE_PORT") != "" {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
	} else {
		// creates the out-of-cluster config
		var kubeconfig *string
		if home := homeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()

		// use the current context in kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}

	}

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	return &Metrics{
		metrics: map[string]*prometheus.Desc{
			"container_health_check_duration_millisecond": newGlobalMetric("container_health_check_duration_millisecond", "The time(millisecond) taken to invoke the health check interface", []string{"namespace", "container_name", "pod_name"}),
		},
		clientset:  clientset,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
}

/**
 * 接口：Describe
 * 功能：传递结构体中的指标描述符到channel
 */
func (c *Metrics) Describe(ch chan<- *prometheus.Desc) {
	// 描述信息（value值 写入 *prometheus.Desc）
	for _, m := range c.metrics {
		ch <- m
	}
}

/**
 * 接口：Collect
 * 功能：抓取最新的数据，传递给channel
 */
func (c *Metrics) Collect(ch chan<- prometheus.Metric) {

	/*
		使用了互斥锁来保护两个共享资源：
			1、Metrics 结构体中的 clientset 字段：假设 clientset 是一个用于与 Kubernetes API 交互的客户端集合，
				可能会被多个 goroutine 同时访问。通过在访问 clientset 之前加锁，确保了在同一时间只有一个 goroutine
				能够访问 clientset，避免了对 clientset 的并发访问导致的竞态条件和数据竞争问题。
			2、ch 通道：ch 是一个用于传递指标数据的通道，可能会被多个 goroutine 同时操作。通过在向 ch 发送数据之前加锁，
				确保了在同一时间只有一个 goroutine 能够向 ch 发送数据，避免了多个 goroutine 同时向 ch 发送数据导致的数据竞争问题。
	*/
	c.mutex.Lock() // 加锁
	defer c.mutex.Unlock()

	pods, err := c.clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	items := pods.Items
	/*
		sync.WaitGroup 用于等待一组 goroutine 完成任务的同步机制。它的作用是确保在一组 goroutine 中的所有任务都完成后，
			主 goroutine 才能继续执行。
		var wg sync.WaitGroup 声明了一个 WaitGroup 对象，用于等待所有的健康检查 goroutine 完成任务

		在代码中的作用体现如下：
		1、在 for 循环之外声明 WaitGroup 对象 wg，表示需要等待多个 goroutine 完成任务。
		2、在 for 循环中，每启动一个新的健康检查 goroutine，都会调用 wg.Add(1) 方法，表示需要等待一个 goroutine 完成任务。
		3、在每个健康检查 goroutine 中，完成任务后都会调用 wg.Done() 方法，表示一个 goroutine 已经完成任务。
		4、在主 goroutine 中，调用 wg.Wait() 方法，等待所有的健康检查 goroutine 完成任务。只有当所有的 goroutine
			都调用了 wg.Done() 方法后，wg.Wait() 方法才会返回，主 goroutine 才能继续执行。
	*/
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		tmp := item
		/*
			实现Collect方法，将pods健康信息写入ch(即 prometheus.Metric)
		*/
		go healthCheck(&tmp, c, ch, &wg)
	}

	wg.Wait()
}

func healthCheck(pod *coreV1.Pod, c *Metrics, ch chan<- prometheus.Metric, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()

	meta := pod.ObjectMeta
	spec := pod.Spec
	status := pod.Status
	podName := meta.Name
	labels := meta.Labels
	containerName := labels["app"]

	livenessProbe := spec.Containers[0].LivenessProbe

	if livenessProbe != nil && livenessProbe.HTTPGet != nil {
		podIP := status.PodIP
		httpGet := livenessProbe.HTTPGet

		start := time.Now()

		var scheme string
		if coreV1.URISchemeHTTP == httpGet.Scheme {
			scheme = "http://"
		} else {
			scheme = "https://"
		}

		resp, err := c.httpClient.Get(scheme + podIP + ":" + strconv.Itoa(int(httpGet.Port.IntVal)) + httpGet.Path)

		var duration time.Duration
		if err != nil {
			duration = -1
		} else {
			duration = time.Since(start)
		}

		if resp != nil {
			defer resp.Body.Close()
		}
		metric := prometheus.MustNewConstMetric(c.metrics["container_health_check_duration_millisecond"], prometheus.GaugeValue, float64(duration), meta.Namespace, containerName, podName)
		// 添加时间戳 container_health_check_duration_millisecond{container_name="",namespace="kube-system",
		// pod_name="cilium-mk95x"} -1 1715059230118（时间戳）
		ch <- prometheus.NewMetricWithTimestamp(time.Now(), metric)

	}

}
