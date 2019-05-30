package appserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	deploymentconfigv1 "github.com/openshift/api/apps/v1"
	routev1 "github.com/openshift/api/route/v1"
	ocappsclient "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/redhat-developer/app-service/appserver/topology"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/redhat-developer/app-service/appserver/watcher"
)

var k8log = logf.Log

type myNodes struct {
	nodes map[string]map[string][]string
}

type nodeMeta struct {
	Id   string
	Name string
	Type string
}

type data struct {
	nodes map[watcher.DataTypes][]watcher.DataTypes
}

type KubeClient struct {
	CoreClient kubernetes.Interface
	OcClient   ocappsclient.AppsV1Interface
}

type Watcher struct {
	Client       *KubeClient
	ResultStream chan watch.Event
	Namespace    string
}

// HandleTopology returns the handler function for the /status endpoint
func (srv *AppServer) HandleTopology() http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {
		/*
			INFO Access the OpenShift web-console here: https://console-openshift-console.apps.tkurian15.devcluster.openshift.com
			INFO Login to the console with user: kubeadmin, password: dN77I-KUXKu-aFsqo-Bn4Ns
		*/

		wat := watcher.NewWatcher("default")
		wat.StartWatcher()
		junkNodes := wat.ListenWatcher()
		var d data
		d.nodes = junkNodes
		namespace := "default"
		host := "https://api.tkurian15.devcluster.openshift.com:6443"
		bearerToken := "Q3aZZ_ZPEN23zP6Dp47RXj_ITKufzwckOr87scxFhd0"

		openshiftAPIConfig := getOpenshiftAPIConfig(host, bearerToken)

		cl, _ := kubernetes.NewForConfig(&openshiftAPIConfig)
		clDepConfig, _ := ocappsclient.NewForConfig(&openshiftAPIConfig)
		clRoute, _ := routeclientset.NewForConfig(&openshiftAPIConfig)
		//dc := getNodes(clDepConfig, cl, namespace)
		//dc := getJunkNodes(junkNodes)
		nodes := d.getUniqueNodes()
		groups := d.getGroups()
		edges := d.getEdges()
		formattedDc := d.formatNodes()

		resources := getResources(nodes, cl, clRoute, clDepConfig, namespace)
		result := topology.GetSampleTopology(formattedDc, resources, groups, edges)

		w.Header().Set(http.CanonicalHeaderKey("Content-Type"), "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}
}

func getOpenshiftAPIConfig(host string, bearerToken string) rest.Config {
	return rest.Config{
		Host:        host,
		BearerToken: bearerToken,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}
}

// func getNodes(clDepConfig *ocappsclient.AppsV1Client, cl *kubernetes.Clientset, namespace string) data {
// 	// Store all node types
// 	var n []interface{}

// 	deploymentConfigs, err := clDepConfig.DeploymentConfigs(namespace).List(metav1.ListOptions{})
// 	if err != nil {
// 		k8log.Error(err, "failed to retrieve json encoding of node")
// 	}
// 	n = append(n, deploymentConfigs.Items)

// 	deployments, err := cl.AppsV1().Deployments(namespace).List(metav1.ListOptions{})
// 	if err != nil {
// 		k8log.Error(err, "failed to retrieve json encoding of node")
// 	}
// 	n = append(n, deployments.Items)

// 	return data{nodes: n}
// }

func (d data) getUniqueNodes() map[string][]string {
	fmt.Println(d.nodes)
	fmt.Println("YUPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPPP")
	return d.getLabelData("app.kubernetes.io/name", "", true)
}

func (d data) getEdges() []string {
	var edges []string
	sourceObjects := make(map[string][]string)
	targetObjects := make(map[string][]string)

	// Arrange keys and target objects.
	targetObjects = d.getAnnotationData("app.openshift.io/connects-to")

	// Arrange keys and source objects.
	for targetKey, _ := range targetObjects {
		sourceObjects[targetKey] = append(sourceObjects[targetKey], d.getLabelData("app.kubernetes.io/name", targetKey, true)[targetKey]...)
	}

	// Lookup the target key in the source key and
	// construct the edge.
	for targetKey, targets := range targetObjects {
		sourceObjects := sourceObjects[targetKey]

		for _, target := range targets {
			for _, source := range sourceObjects {
				var nm nodeMeta
				err := json.Unmarshal([]byte(source), &nm)
				if err != nil {
					k8log.Error(err, "failed to get node data")
				}

				e, err := json.Marshal(topology.Edge{Source: nm.Id, Target: target})
				if err != nil {
					k8log.Error(err, "failed to retrieve json encoding of node")
				}
				edges = append(edges, string(e))
			}
		}
	}

	return edges
}

func (d data) getGroups() []string {
	nodes := make(map[string][]string)
	var groups []string

	nodes = d.getLabelData("app.kubernetes.io/part-of", "", false)

	for key, value := range nodes {
		g, err := json.Marshal(topology.Group{Id: "group:" + key, Name: key, Nodes: value})
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of node")
		}
		groups = append(groups, string(g))
	}

	return groups
}

func getResources(dc map[string][]string, cl *kubernetes.Clientset, clRoute *routeclientset.RouteV1Client, clDepConfig *ocappsclient.AppsV1Client, namespace string) map[string]string {
	nodeDatas := make(map[string]string)
	for labelKey, dcNodes := range dc {
		options := metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s", labelKey),
		}
		for _, dc := range dcNodes {
			var nm nodeMeta
			err := json.Unmarshal([]byte(dc), &nm)
			if err != nil {
				k8log.Error(err, "failed to list existing replicationControllers")
			}

			//Replication Controllers
			replicationControllers, err := cl.CoreV1().ReplicationControllers(namespace).List(options)
			if err != nil {
				k8log.Error(err, "failed to list existing deployment configs")
			}
			resources := formatReplicationControllers(replicationControllers.Items)
			//Services
			services, err := cl.CoreV1().Services(namespace).List(options)
			if err != nil {
				k8log.Error(err, "failed to list existing deployment configs")
			}
			resources = append(resources, formatService(services.Items)...)

			// Routes
			routes, err := clRoute.Routes(namespace).List(options)
			if err != nil {
				k8log.Error(err, "failed to list existing routes")
			}
			resources = append(resources, formatRoutes(routes.Items)...)

			// DeploymentConfigs
			deploymentConfigs, err := clDepConfig.DeploymentConfigs(namespace).List(options)
			if err != nil {
				k8log.Error(err, "failed to list existing deployment configs")
			}
			resources = append(resources, formatDeploymentConfigs(deploymentConfigs.Items)...)

			nd, err := json.Marshal(topology.NodeData{Id: nm.Id, Type: nm.Type, Resources: resources, Data: topology.Data{Url: "dummy_url", EditUrl: "dummy_edit_url", BuilderImage: labelKey, DonutStatus: make(map[string]string)}})
			if err != nil {
				k8log.Error(err, "failed to retrieve json encoding of node")
			}
			nodeDatas[nm.Id] = string(nd)
		}
	}
	return nodeDatas
}

func formatDeploymentConfigs(deploymentConfigItems []deploymentconfigv1.DeploymentConfig) []topology.Resource {
	var resources []topology.Resource

	for _, elem := range deploymentConfigItems {
		meta, err := json.Marshal(elem.GetObjectMeta())
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of deployment config")
		}
		status, err := json.Marshal(elem.Status)
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of deployment config")
		}
		resources = append(resources, topology.Resource{Name: elem.Name, Kind: elem.Kind, Metadata: string(meta), Status: string(status)})
	}
	return resources
}

func formatService(services []corev1.Service) []topology.Resource {
	var resources []topology.Resource

	for _, elem := range services {
		meta, err := json.Marshal(elem.GetObjectMeta())
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of service")
		}
		status, err := json.Marshal(elem.Status)
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of service")
		}
		resources = append(resources, topology.Resource{Name: elem.Name, Kind: elem.Kind, Metadata: string(meta), Status: string(status)})
	}
	return resources
}

func formatReplicationControllers(replicaSetItems []corev1.ReplicationController) []topology.Resource {
	var resources []topology.Resource

	for _, elem := range replicaSetItems {
		meta, err := json.Marshal(elem.GetObjectMeta())
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of replication controller")
		}
		status, err := json.Marshal(elem.Status)
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of replication controller")
		}
		resources = append(resources, topology.Resource{Name: elem.Name, Kind: elem.Kind, Metadata: string(meta), Status: string(status)})
	}
	return resources
}

func formatRoutes(routeItems []routev1.Route) []topology.Resource {
	var resources []topology.Resource

	for _, elem := range routeItems {
		meta, err := json.Marshal(elem.GetObjectMeta())
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of route")
		}
		status, err := json.Marshal(elem.Status)
		if err != nil {
			k8log.Error(err, "failed to retrieve json encoding of route")
		}
		resources = append(resources, topology.Resource{Name: elem.Name, Kind: elem.Kind, Metadata: string(meta), Status: string(status)})
	}
	return resources
}

func (d data) formatNodes() []string {
	var nodes []string

	for key, _ := range d.nodes {
		if key.Key == "DeploymentConfig" {
			dc := key.Value.(*deploymentconfigv1.DeploymentConfig)
			n, err := json.Marshal(topology.Node{Name: dc.Name, Id: base64.StdEncoding.EncodeToString([]byte(dc.UID))})
			if err != nil {
				k8log.Error(err, "failed to retrieve json encoding of node")
			}
			nodes = append(nodes, string(n))
		} else if key.Key == "Deployment" {
			d := key.Value.(*appsv1.Deployment)
			n, err := json.Marshal(topology.Node{Name: d.Name, Id: base64.StdEncoding.EncodeToString([]byte(d.UID))})
			if err != nil {
				k8log.Error(err, "failed to retrieve json encoding of node")
			}
			nodes = append(nodes, string(n))
		}
	}

	// switch elem.(type) {
	// case []deploymentconfigv1.DeploymentConfig:
	// 	deploymentsConfigs := elem.([]deploymentconfigv1.DeploymentConfig)
	// 	for _, dc := range deploymentsConfigs {
	// 		n, err := json.Marshal(topology.Node{Name: dc.Name, Id: base64.StdEncoding.EncodeToString([]byte(dc.UID))})
	// 		if err != nil {
	// 			k8log.Error(err, "failed to retrieve json encoding of node")
	// 		}
	// 		nodes = append(nodes, string(n))
	// 	}
	// case []appsv1.Deployment:
	// 	deployments := elem.([]appsv1.Deployment)
	// 	for _, d := range deployments {
	// 		n, err := json.Marshal(topology.Node{Name: d.Name, Id: base64.StdEncoding.EncodeToString([]byte(d.UID))})
	// 		if err != nil {
	// 			k8log.Error(err, "failed to retrieve json encoding of node")
	// 		}
	// 		nodes = append(nodes, string(n))
	// 	}
	// }
	//}

	return nodes
}

func (d data) getLabelData(label string, keyLabel string, meta bool) map[string][]string {
	nnn := make(map[string][]string)
	fmt.Println("STARTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTTT")
	for key, _ := range d.nodes {
		fmt.Println("==========================================================")
		if key.Key == "DeploymentConfig" {
			fmt.Println("HEREEEEEEEEEE")
			fmt.Println(key)
			fmt.Println("HEREEEEEEEEEE")
			dc := key.Value.(*deploymentconfigv1.DeploymentConfig)
			labelValue := dc.Labels[label]
			var jsn []byte
			var err error
			if meta {
				fmt.Println("METAAAAAAAAAA")
				jsn, err = json.Marshal(nodeMeta{Name: dc.Name, Type: "workload", Id: base64.StdEncoding.EncodeToString([]byte(dc.UID))})
				if err != nil {
					k8log.Error(err, "failed to retrieve json encoding of node")
				}
			} else {
				jsn, err = json.Marshal(dc.UID)
				if err != nil {
					k8log.Error(err, "failed to retrieve json encoding of node")
				}
			}

			fmt.Println(keyLabel)
			fmt.Println(labelValue)
			if keyLabel == "" {
				nnn[labelValue] = append(nnn[labelValue], string(jsn))
			} else if keyLabel == labelValue {
				fmt.Println("YESSSSSSSSSSSSSSS")
				nnn[labelValue] = append(nnn[labelValue], string(jsn))
			}
		} else if key.Key == "Deployment" {
			d := key.Value.(*appsv1.Deployment)
			labelValue := d.Labels[label]
			jsn, err := json.Marshal(d.UID)
			if err != nil {
				k8log.Error(err, "failed to retrieve json encoding of node")
			}

			if keyLabel == "" {
				nnn[labelValue] = append(nnn[labelValue], string(jsn))
			} else if keyLabel == labelValue {
				nnn[labelValue] = append(nnn[labelValue], string(jsn))
			}
		}
	}
	// 	switch elem.(type) {
	// 	case []deploymentconfigv1.DeploymentConfig:
	// 		deploymentsConfigs := elem.([]deploymentconfigv1.DeploymentConfig)
	// 		for _, dc := range deploymentsConfigs {
	// 			labelValue := dc.Labels[label]
	// 			var jsn []byte
	// 			var err error
	// 			if meta {
	// 				jsn, err = json.Marshal(nodeMeta{Name: dc.Name, Type: "workload", Id: base64.StdEncoding.EncodeToString([]byte(dc.UID))})
	// 				if err != nil {
	// 					k8log.Error(err, "failed to retrieve json encoding of node")
	// 				}
	// 			} else {
	// 				jsn, err = json.Marshal(dc.UID)
	// 				if err != nil {
	// 					k8log.Error(err, "failed to retrieve json encoding of node")
	// 				}
	// 			}

	// 			if key == "" {
	// 				nodes[labelValue] = append(nodes[labelValue], string(jsn))
	// 			} else if key == labelValue {
	// 				nodes[labelValue] = append(nodes[labelValue], string(jsn))
	// 			}
	// 		}
	// 	case []appsv1.Deployment:
	// 		deployments := elem.([]appsv1.Deployment)
	// 		for _, d := range deployments {
	// 			labelValue := d.Labels[label]
	// 			jsn, err := json.Marshal(d.UID)
	// 			if err != nil {
	// 				k8log.Error(err, "failed to retrieve json encoding of node")
	// 			}

	// 			if key == "" {
	// 				nodes[labelValue] = append(nodes[labelValue], string(jsn))
	// 			} else if key == labelValue {
	// 				nodes[labelValue] = append(nodes[labelValue], string(jsn))
	// 			}
	// 		}
	// 	}
	// }
	fmt.Println(")))))))))))))))))))))))))))))))))))))))))))))))))))))")
	fmt.Println(nnn)
	fmt.Println(")))))))))))))))))))))))))))))))))))))))))))))))))))))")
	return nnn
}

func (d data) getAnnotationData(annotation string) map[string][]string {
	nodes := make(map[string][]string)
	for key, _ := range d.nodes {
		if key.Key == "DeploymentConfig" {
			dc := key.Value.(*deploymentconfigv1.DeploymentConfig)
			var keys []string
			err := json.Unmarshal([]byte(dc.Annotations[annotation]), &keys)
			if err != nil {
				k8log.Error(err, "failed to retrieve json dencoding of node")
			}
			for _, key := range keys {
				json, err := json.Marshal(dc.UID)
				if err != nil {
					k8log.Error(err, "failed to retrieve json dencoding of node")
				}
				nodes[key] = append(nodes[key], string(json))
			}
		} else if key.Key == "Deployment" {
			d := key.Value.(*appsv1.Deployment)
			var keys []string
			err := json.Unmarshal([]byte(d.Annotations[annotation]), &keys)
			if err != nil {
				k8log.Error(err, "failed to retrieve json dencoding of node")
			}
			for _, key := range keys {
				jsn, err := json.Marshal(d.UID)
				if err != nil {
					k8log.Error(err, "failed to retrieve json dencoding of node")
				}
				nodes[key] = append(nodes[key], string(jsn))
			}
		}

		// switch elem.(type) {
		// case []deploymentconfigv1.DeploymentConfig:
		// 	deploymentsConfigs := elem.([]deploymentconfigv1.DeploymentConfig)
		// 	for _, dc := range deploymentsConfigs {
		// 		var keys []string
		// 		err := json.Unmarshal([]byte(dc.Annotations[annotation]), &keys)
		// 		if err != nil {
		// 			k8log.Error(err, "failed to retrieve json dencoding of node")
		// 		}
		// 		for _, key := range keys {
		// 			json, err := json.Marshal(dc.UID)
		// 			if err != nil {
		// 				k8log.Error(err, "failed to retrieve json dencoding of node")
		// 			}
		// 			nodes[key] = append(nodes[key], string(json))
		// 		}
		// 	}
		// case []appsv1.Deployment:
		// 	deployments := elem.([]appsv1.Deployment)
		// 	for _, d := range deployments {
		// 		var keys []string
		// 		err := json.Unmarshal([]byte(d.Annotations[annotation]), &keys)
		// 		if err != nil {
		// 			k8log.Error(err, "failed to retrieve json dencoding of node")
		// 		}
		// 		for _, key := range keys {
		// 			jsn, err := json.Marshal(d.UID)
		// 			if err != nil {
		// 				k8log.Error(err, "failed to retrieve json dencoding of node")
		// 			}
		// 			nodes[key] = append(nodes[key], string(jsn))
		// 		}
		// 	}
		// }
	}

	return nodes
}
