package operators

import (
	"fmt"
	"os/exec"
	"regexp"

	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"

	"path/filepath"
	"strings"
	"time"

	exutil "github.com/openshift/openshift-tests/test/extended/util"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

var _ = g.Describe("[sig-operators] OLM should", func() {
	defer g.GinkgoRecover()

	var oc = exutil.NewCLIWithoutNamespace("default")

	operators := "operators.coreos.com"
	providedAPIs := []struct {
		fromAPIService bool
		group          string
		version        string
		plural         string
	}{
		{
			fromAPIService: true,
			group:          "packages." + operators,
			version:        "v1",
			plural:         "packagemanifests",
		},
		{
			group:   operators,
			version: "v1",
			plural:  "operatorgroups",
		},
		{
			group:   operators,
			version: "v1alpha1",
			plural:  "clusterserviceversions",
		},
		{
			group:   operators,
			version: "v1alpha1",
			plural:  "catalogsources",
		},
		{
			group:   operators,
			version: "v1alpha1",
			plural:  "installplans",
		},
		{
			group:   operators,
			version: "v1alpha1",
			plural:  "subscriptions",
		},
	}

	for i := range providedAPIs {
		api := providedAPIs[i]
		g.It(fmt.Sprintf("Author:jiazha-High-36803-OLM is installed with %s at version %s", api.plural, api.version), func() {
			if api.fromAPIService {
				// Ensure spec.version matches expected
				raw, err := oc.AsAdmin().Run("get").Args("apiservices", fmt.Sprintf("%s.%s", api.version, api.group), "-o=jsonpath={.spec.version}").Output()
				o.Expect(err).NotTo(o.HaveOccurred())
				o.Expect(raw).To(o.Equal(api.version))
			} else {
				// Ensure expected version exists in spec.versions and is both served and stored
				raw, err := oc.AsAdmin().Run("get").Args("crds", fmt.Sprintf("%s.%s", api.plural, api.group), fmt.Sprintf("-o=jsonpath={.spec.versions[?(@.name==\"%s\")]}", api.version)).Output()
				o.Expect(err).NotTo(o.HaveOccurred())
				o.Expect(raw).To(o.ContainSubstring("\"served\":true"))
				o.Expect(raw).To(o.ContainSubstring("\"storage\":true"))
			}
		})
	}

	// author: bandrade@redhat.com
	g.It("Author:bandrade-High-24061-have imagePullPolicy:IfNotPresent on thier deployments", func() {
		deploymentResource := []string{"catalog-operator", "olm-operator", "packageserver"}
		for _, v := range deploymentResource {
			msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", "openshift-operator-lifecycle-manager", "deployment", v, "-o=jsonpath={.spec.template.spec.containers[*].imagePullPolicy}").Output()
			e2e.Logf("%s.imagePullPolicy:%s", v, msg)
			if err != nil {
				e2e.Failf("Unable to get %s, error:%v", msg, err)
			}
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(msg).To(o.Equal("IfNotPresent"))
		}
	})

	// author: bandrade@redhat.com
	g.It("Author:bandrade-High-24829-Report Upgradeable in OLM ClusterOperators status", func() {
		olmCOs := []string{"operator-lifecycle-manager", "operator-lifecycle-manager-catalog", "operator-lifecycle-manager-packageserver"}
		for _, co := range olmCOs {
			msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("co", co, "-o=jsonpath={range .status.conditions[*]}{.type}{' '}{.status}").Output()
			if err != nil {
				e2e.Failf("Unable to get co %s status, error:%v", msg, err)
			}
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(msg).To(o.ContainSubstring("Upgradeable True"))
		}
	})

	// author: tbuskey@redhat.com
	g.It("Author:tbuskey-High-27589-do not use ipv4 addresses in CatalogSources generated by marketplace", func() {
		re := regexp.MustCompile(`(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)){3}`)
		olmErrs := 0
		olmNames := []string{""}
		olmNamespace := "openshift-marketplace"
		olmJpath := "-o=jsonpath={range .items[*]}{@.metadata.name}{','}{@.spec.address}{'\\n'}"
		msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("catalogsource", "-n", olmNamespace, olmJpath).Output()
		if err != nil {
			e2e.Failf("Unable to get pod -n %v %v.", olmNamespace, olmJpath)
		}
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(msg).NotTo(o.ContainSubstring("No resources found"))
		// msg = fmt.Sprintf("%v\ntest,1.1.1.1\n", msg)
		lines := strings.Split(msg, "\n")
		for _, line := range lines {
			if len(line) <= 0 {
				continue
			}
			name := strings.Split(line, ",")
			cscAddr := strings.Split(name[1], ":")[0]
			if re.MatchString(cscAddr) {
				olmErrs++
				olmNames = append(olmNames, name[0])
			}
		}
		if olmErrs > 0 {
			e2e.Failf("%v ipv4 addresses found in these OLM components: %v", olmErrs, olmNames)
		}
	})

})

var _ = g.Describe("[sig-operators] OLM for an end user use", func() {
	defer g.GinkgoRecover()

	var oc = exutil.NewCLI("olm", exutil.KubeConfigPath())

	// author: jiazha@redhat.com
	g.It("Author:jiazha-Critical-23440-can subscribe to the etcd operator  [Serial]", func() {
		buildPruningBaseDir := exutil.FixturePath("testdata", "olm")
		etcdCluster := filepath.Join(buildPruningBaseDir, "etcd-cluster.yaml")
		subTemplate := filepath.Join(buildPruningBaseDir, "olm-subscription.yaml")
		oc.SetupProject()

		dr := make(describerResrouce)
		itName := g.CurrentGinkgoTestDescription().TestText
		dr.addIr(itName)

		g.By("Cluster-admin start to subscribe to etcd operator")
		sub := subscriptionDescription{
			subName:                "sub-23440",
			namespace:              "openshift-operators",
			catalogSourceName:      "community-operators",
			catalogSourceNamespace: "openshift-marketplace",
			channel:                "clusterwide-alpha",
			ipApproval:             "Automatic",
			operatorPackage:        "etcd",
			singleNamespace:        false,
			template:               subTemplate,
		}
		defer sub.delete(itName, dr)
		sub.createWithoutCheck(oc, itName, dr)

		g.By("try to get installPlan and CSV of the sub")
		var installedPlan string
		errIP := wait.Poll(10*time.Second, 150*time.Second, func() (bool, error) {
			output := getResource(oc, asAdmin, withoutNamespace, "ip", "-n", "openshift-operators", "-o=jsonpath={.items[*].metadata.name}")
			ips := strings.Fields(output)
			if len(ips) == 0 {
				e2e.Logf("no ip is found, and try next")
				return false, nil
			}

			for _, ip := range ips {
				subNames := getResource(oc, asAdmin, withoutNamespace, "ip", ip, "-n", "openshift-operators", "-o=jsonpath={.metadata.ownerReferences[*].name}")
				if strings.Contains(subNames, sub.subName) {
					e2e.Logf("found installplan and try to get csv")
					installedPlan = ip
					return true, nil
				}

			}
			return false, nil
		})
		o.Expect(errIP).NotTo(o.HaveOccurred())
		installedCSVs := getResource(oc, asAdmin, withoutNamespace, "ip", installedPlan, "-n", "openshift-operators", "-o=jsonpath={.spec.clusterServiceVersionNames[*]}")
		o.Expect(installedCSVs).NotTo(o.BeEmpty())
		sub.installedCSV = strings.Fields(installedCSVs)[0]
		dr.getIr(itName).add(newResource(oc, "csv", sub.installedCSV, requireNS, sub.namespace))

		defer sub.getCSV().delete(itName, dr)
		newCheck("expect", asAdmin, true, compare, "Succeeded", ok, []string{"csv", sub.installedCSV, "-n", "openshift-operators", "-o=jsonpath={.status.phase}"}).check(oc)

		g.By("Switch to common user to create the resources provided by the operator")
		etcdClusterName := "example-etcd-cluster"
		configFile, err := oc.Run("process").Args("-f", etcdCluster, "-p", fmt.Sprintf("NAME=%s", etcdClusterName), fmt.Sprintf("NAMESPACE=%s", oc.Namespace())).OutputToFile("config.json")
		o.Expect(err).NotTo(o.HaveOccurred())

		defer func() {
			_, err := oc.Run("delete").Args("etcdcluster", etcdClusterName, "-n", oc.Namespace()).Output()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		err = oc.Run("create").Args("-f", configFile).Execute()
		o.Expect(err).NotTo(o.HaveOccurred())

		newCheck("expect", false, true, compare, "Running", ok, []string{"etcdCluster", etcdClusterName, "-n", oc.Namespace(), "-o=jsonpath={.status.phase}"}).check(oc)
	})
})

var _ = g.Describe("[sig-operators] OLM for an end user handle within a namespace", func() {
	defer g.GinkgoRecover()

	var (
		oc = exutil.NewCLI("olm-a-"+getRandomString(), exutil.KubeConfigPath())

		buildPruningBaseDir = exutil.FixturePath("testdata", "olm")
		ogSingleTemplate    = filepath.Join(buildPruningBaseDir, "operatorgroup.yaml")
		subTemplate         = filepath.Join(buildPruningBaseDir, "olm-subscription.yaml")
		dr                  = make(describerResrouce)

		ogD = operatorGroupDescription{
			name:      "og-singlenamespace",
			namespace: "",
			template:  ogSingleTemplate,
		}
		subD = subscriptionDescription{
			subName:                "hawtio-operator",
			namespace:              "",
			channel:                "alpha",
			ipApproval:             "Automatic",
			operatorPackage:        "hawtio-operator",
			catalogSourceName:      "community-operators",
			catalogSourceNamespace: "openshift-marketplace",
			startingCSV:            "",
			currentCSV:             "",
			installedCSV:           "",
			template:               subTemplate,
			singleNamespace:        true,
		}
	)

	g.BeforeEach(func() {
		itName := g.CurrentGinkgoTestDescription().TestText
		dr.addIr(itName)
	})

	g.AfterEach(func() {})

	// It will cover test case: OCP-24870, author: kuiwang@redhat.com
	g.It("ConnectedOnly-Author:kuiwang-High-24870-can not create csv without operator group", func() {
		var (
			itName = g.CurrentGinkgoTestDescription().TestText
			og     = ogD
			sub    = subD
		)
		oc.SetupProject() //project and its resource are deleted automatically when out of It, so no need derfer or AfterEach
		og.namespace = oc.Namespace()
		sub.namespace = oc.Namespace()

		g.By("Create csv with failure because of no operator group")
		sub.currentCSV = "hawtio-operator.v0.2.0"
		sub.createWithoutCheck(oc, itName, dr)
		newCheck("present", asUser, withNamespace, notPresent, "", ok, []string{"csv", sub.currentCSV}).check(oc)
		sub.delete(itName, dr)

		g.By("Create opertor group and then csv is created with success")
		og.create(oc, itName, dr)
		sub.create(oc, itName, dr)
		newCheck("expect", asUser, withNamespace, compare, "Succeeded"+"InstallSucceeded", ok, []string{"csv", sub.installedCSV, "-o=jsonpath={.status.phase}{.status.reason}"}).check(oc)
	})

	// It will cover part of test case: OCP-25855, author: kuiwang@redhat.com
	g.It("ConnectedOnly-Author:kuiwang-High-25855-Add the channel field to subscription_sync_count", func() {
		var (
			itName = g.CurrentGinkgoTestDescription().TestText
			og     = ogD
			sub    = subD
		)
		oc.SetupProject() //project and its resource are deleted automatically when out of It, so no need derfer or AfterEach
		og.namespace = oc.Namespace()
		sub.namespace = oc.Namespace()

		g.By("Create og")
		og.create(oc, itName, dr)

		g.By("Create operator")
		sub.create(oc, itName, dr)
		newCheck("expect", asUser, withNamespace, compare, "Succeeded", ok, []string{"csv", sub.installedCSV, "-o=jsonpath={.status.phase}"}).check(oc)

		g.By("get information of catalog operator pod")
		output := getResource(oc, asAdmin, withoutNamespace, "pods", "-l", "app=catalog-operator", "-n", "openshift-operator-lifecycle-manager", "-o=jsonpath={.items[0].metadata.name}{\" \"}{.items[0].status.podIP}{\":\"}{.items[0].spec.containers[0].ports[?(@.name==\"metrics\")].containerPort}")
		o.Expect(output).NotTo(o.BeEmpty())
		infoCatalogOperator := strings.Fields(output)

		g.By("check the subscription_sync_total")
		err := wait.Poll(10*time.Second, 120*time.Second, func() (bool, error) {
			subscriptionSyncTotal, _ := exec.Command("bash", "-c", "oc exec -c catalog-operator "+infoCatalogOperator[0]+" -n openshift-operator-lifecycle-manager -- curl -s -k -H 'Authorization: Bearer $(oc sa get-token prometheus-k8s -n openshift-monitoring)' https://"+infoCatalogOperator[1]+"/metrics").Output()
			if !strings.Contains(string(subscriptionSyncTotal), sub.installedCSV) {
				e2e.Logf("the metric is not counted and try next round")
				return false, nil
			}
			return true, nil
		})
		o.Expect(err).NotTo(o.HaveOccurred())
	})

})

var _ = g.Describe("[sig-operators] OLM for an end user handle to support", func() {
	defer g.GinkgoRecover()

	var (
		oc = exutil.NewCLI("olm-cm-"+getRandomString(), exutil.KubeConfigPath())

		buildPruningBaseDir = exutil.FixturePath("testdata", "olm")
		cmNcTemplate        = filepath.Join(buildPruningBaseDir, "cm-namespaceconfig.yaml")
		catsrcCmTemplate    = filepath.Join(buildPruningBaseDir, "catalogsource-configmap.yaml")
		ogAllTemplate       = filepath.Join(buildPruningBaseDir, "og-allns.yaml")
		ogMultiTemplate     = filepath.Join(buildPruningBaseDir, "og-multins.yaml")
		subTemplate         = filepath.Join(buildPruningBaseDir, "olm-subscription.yaml")
		dr                  = make(describerResrouce)

		cmNc = configMapDescription{
			name:      "cm-community-namespaceconfig-operators",
			namespace: "", //must be set in iT
			template:  cmNcTemplate,
		}
		catsrcNc = catalogSourceDescription{
			name:        "catsrc-community-namespaceconfig-operators",
			namespace:   "", //must be set in iT
			displayName: "Community namespaceconfig Operators",
			publisher:   "Community",
			sourceType:  "configmap",
			address:     "cm-community-namespaceconfig-operators",
			template:    catsrcCmTemplate,
		}
		subNc = subscriptionDescription{
			subName:                "namespace-configuration-operator",
			namespace:              "", //must be set in iT
			channel:                "alpha",
			ipApproval:             "Automatic",
			operatorPackage:        "namespace-configuration-operator",
			catalogSourceName:      "catsrc-community-namespaceconfig-operators",
			catalogSourceNamespace: "", //must be set in iT
			startingCSV:            "",
			currentCSV:             "namespace-configuration-operator.v0.1.0", //it matches to that in cm, so set it.
			installedCSV:           "",
			template:               subTemplate,
			singleNamespace:        true,
		}
	)

	g.BeforeEach(func() {
		itName := g.CurrentGinkgoTestDescription().TestText
		dr.addIr(itName)
	})

	g.AfterEach(func() {})

	// It will cover part of test case: OCP-22226, author: kuiwang@redhat.com
	g.It("ConnectedOnly-Author:kuiwang-High-22226-the csv without support AllNamespaces fails for og with allnamespace", func() {
		var (
			itName = g.CurrentGinkgoTestDescription().TestText
			og     = operatorGroupDescription{
				name:      "og-allnamespace",
				namespace: "",
				template:  ogAllTemplate,
			}
			cm     = cmNc
			catsrc = catsrcNc
			sub    = subNc
		)

		//oc.TeardownProject()
		oc.SetupProject() //project and its resource are deleted automatically when out of It, so no need derfer or AfterEach
		cm.namespace = oc.Namespace()
		catsrc.namespace = oc.Namespace()
		sub.namespace = oc.Namespace()
		sub.catalogSourceNamespace = catsrc.namespace
		og.namespace = oc.Namespace()
		g.By("Create cm")
		cm.create(oc, itName, dr)

		g.By("Create catalog source")
		catsrc.create(oc, itName, dr)

		g.By("Create og")
		og.create(oc, itName, dr)

		g.By("Create sub")
		sub.create(oc, itName, dr)
		newCheck("expect", asAdmin, withoutNamespace, contain, "AllNamespaces InstallModeType not supported", ok, []string{"csv", sub.installedCSV, "-n", sub.namespace, "-o=jsonpath={.status.message}"}).check(oc)
	})

	// It will cover part of test case: OCP-22226, author: kuiwang@redhat.com
	g.It("ConnectedOnly-Author:kuiwang-High-22226-the csv without support MultiNamespace fails for og with MultiNamespace", func() {
		var (
			itName = g.CurrentGinkgoTestDescription().TestText
			og     = operatorGroupDescription{
				name:         "og-multinamespace",
				namespace:    "",
				multinslabel: "olmtestmultins",
				template:     ogMultiTemplate,
			}
			p1 = projectDescription{
				name:            "olm-enduser-multins-csv-1-fail",
				targetNamespace: "",
			}
			p2 = projectDescription{
				name:            "olm-enduser-multins-csv-2-fail",
				targetNamespace: "",
			}
			cm     = cmNc
			catsrc = catsrcNc
			sub    = subNc
		)

		defer p1.delete(oc)
		defer p2.delete(oc)
		//oc.TeardownProject()
		oc.SetupProject() //project and its resource are deleted automatically when out of It, so no need derfer or AfterEach
		cm.namespace = oc.Namespace()
		catsrc.namespace = oc.Namespace()
		sub.namespace = oc.Namespace()
		sub.catalogSourceNamespace = catsrc.namespace
		og.namespace = oc.Namespace()
		p1.targetNamespace = oc.Namespace()
		p2.targetNamespace = oc.Namespace()
		g.By("Create new project")
		p1.create(oc, itName, dr)
		p1.label(oc, "olmtestmultins")
		p2.create(oc, itName, dr)
		p2.label(oc, "olmtestmultins")

		g.By("Create cm")
		cm.create(oc, itName, dr)

		g.By("Create catalog source")
		catsrc.create(oc, itName, dr)

		g.By("Create og")
		og.create(oc, itName, dr)

		g.By("Create sub")
		sub.create(oc, itName, dr)
		newCheck("expect", asAdmin, withoutNamespace, contain, "MultiNamespace InstallModeType not supported", ok, []string{"csv", sub.installedCSV, "-n", sub.namespace, "-o=jsonpath={.status.message}"}).check(oc)
	})

})

var _ = g.Describe("[sig-operators] OLM for an end user handle within all namespace", func() {
	defer g.GinkgoRecover()

	var (
		oc = exutil.NewCLI("olm-all-"+getRandomString(), exutil.KubeConfigPath())

		buildPruningBaseDir = exutil.FixturePath("testdata", "olm")
		subTemplate         = filepath.Join(buildPruningBaseDir, "olm-subscription.yaml")
		dr                  = make(describerResrouce)
	)

	g.BeforeEach(func() {
		itName := g.CurrentGinkgoTestDescription().TestText
		dr.addIr(itName)
	})

	g.AfterEach(func() {
		itName := g.CurrentGinkgoTestDescription().TestText
		dr.getIr(itName).cleanup()
		dr.rmIr(itName)
	})

	// It will cover test case: OCP-25679, OCP-21418(acutally it covers OCP-25679), author: kuiwang@redhat.com
	g.It("ConnectedOnly-Author:kuiwang-High-25679-Medium-21418-Cluster resource created and deleted correctly", func() {
		var (
			itName = g.CurrentGinkgoTestDescription().TestText
			sub    = subscriptionDescription{
				subName:                "teiid",
				namespace:              "openshift-operators",
				channel:                "beta",
				ipApproval:             "Automatic",
				operatorPackage:        "teiid",
				catalogSourceName:      "community-operators",
				catalogSourceNamespace: "openshift-marketplace",
				// startingCSV:            "teiid.v0.3.0",
				startingCSV:     "", //get it from package based on currentCSV if ipApproval is Automatic
				currentCSV:      "",
				installedCSV:    "",
				template:        subTemplate,
				singleNamespace: false,
			}
			crdName      = "virtualdatabases.teiid.io"
			crName       = "VirtualDatabase"
			podLabelName = "teiid"
			cl           = checkList{}
		)

		// OCP-25679, OCP-21418
		g.By("Create operator targeted at all namespace")
		sub.create(oc, itName, dr)

		// OCP-25679, OCP-21418
		g.By("Check the cluster resource rolebinding, role and service account exists")
		clusterResources := strings.Fields(getResource(oc, asAdmin, withoutNamespace, "clusterrolebinding",
			fmt.Sprintf("--selector=olm.owner=%s", sub.installedCSV), "-o=jsonpath={.items[0].metadata.name}{\" \"}{.items[0].roleRef.name}{\" \"}{.items[0].subjects[0].name}"))
		o.Expect(clusterResources).NotTo(o.BeEmpty())
		cl.add(newCheck("present", asAdmin, withoutNamespace, present, "", ok, []string{"clusterrole", clusterResources[1]}))
		cl.add(newCheck("present", asAdmin, withoutNamespace, present, "", ok, []string{"sa", clusterResources[2], "-n", sub.namespace}))

		// OCP-21418
		g.By("Check the pods of the operator is running")
		cl.add(newCheck("expect", asAdmin, withoutNamespace, contain, "Running", ok, []string{"pod", fmt.Sprintf("--selector=name=%s", podLabelName), "-n", sub.namespace, "-o=jsonpath={.items[*].status.phase}"}))

		// OCP-21418
		g.By("Check no resource of new crd")
		cl.add(newCheck("present", asAdmin, withNamespace, notPresent, "", ok, []string{crName}))
		//do check parallelly
		cl.check(oc)
		cl.empty()

		// OCP-25679, OCP-21418
		g.By("Delete the operator")
		sub.delete(itName, dr)
		sub.getCSV().delete(itName, dr)

		// OCP-25679, OCP-21418
		g.By("Check the cluster resource rolebinding, role and service account do not exist")
		cl.add(newCheck("present", asAdmin, withoutNamespace, notPresent, "", ok, []string{"clusterrolebinding", clusterResources[0]}))
		cl.add(newCheck("present", asAdmin, withoutNamespace, notPresent, "", ok, []string{"clusterrole", clusterResources[1]}))
		cl.add(newCheck("present", asAdmin, withoutNamespace, notPresent, "", ok, []string{"sa", clusterResources[2], "-n", sub.namespace}))

		// OCP-21418
		g.By("Check the CRD still exists")
		cl.add(newCheck("present", asAdmin, withoutNamespace, present, "", ok, []string{"crd", crdName}))

		// OCP-21418
		g.By("Check the pods of the operator is deleted")
		cl.add(newCheck("expect", asAdmin, withoutNamespace, compare, "", ok, []string{"pod", fmt.Sprintf("--selector=name=%s", podLabelName), "-n", sub.namespace, "-o=jsonpath={.items[*].status.phase}"}))

		cl.check(oc)

	})

	// It will cover test case: OCP-25783, author: kuiwang@redhat.com
	g.It("ConnectedOnly-Author:kuiwang-High-25783-Subscriptions are not getting processed taking very long to get processed [Serial]", func() {
		var (
			itName           = g.CurrentGinkgoTestDescription().TestText
			subElasticSearch = subscriptionDescription{
				subName:                "elasticsearch-operator",
				namespace:              "openshift-operators",
				channel:                "preview",
				ipApproval:             "Automatic",
				operatorPackage:        "elasticsearch-operator",
				catalogSourceName:      "redhat-operators",
				catalogSourceNamespace: "openshift-marketplace",
				// startingCSV:            "elasticsearch-operator.4.1.37-202003021622",
				startingCSV: "", //get it from package based on currentCSV if ipApproval is Automatic
				// currentCSV:  "",
				currentCSV:      "elasticsearch-operator.4.1.41-202004130646",
				installedCSV:    "",
				template:        subTemplate,
				singleNamespace: false,
			}

			csvElasticSearch = csvDescription{
				name:      "",
				namespace: "openshift-operators",
			}

			subJaeger = subscriptionDescription{
				subName:                "jaeger-product",
				namespace:              "openshift-operators",
				channel:                "stable",
				ipApproval:             "Automatic",
				operatorPackage:        "jaeger-product",
				catalogSourceName:      "redhat-operators",
				catalogSourceNamespace: "openshift-marketplace",
				// startingCSV:            "jaeger-operator.v1.17.1",
				startingCSV:     "", //get it from package based on currentCSV if ipApproval is Automatic
				currentCSV:      "",
				installedCSV:    "",
				template:        subTemplate,
				singleNamespace: false,
			}

			csvJaeger = csvDescription{
				name:      "",
				namespace: "openshift-operators",
			}

			crdJaegers = crdDescription{
				name:     "jaegers.jaegertracing.io",
				template: "",
			}

			crdElasticSearch = crdDescription{
				name:     "elasticsearches.logging.openshift.io",
				template: "",
			}
		)

		// check ElasticSearch, Jaeger exit and if existing, return
		output, err := doAction(oc, "get", asAdmin, withoutNamespace, "crd", crdElasticSearch.name, "--ignore-not-found")
		o.Expect(err).NotTo(o.HaveOccurred())
		if strings.Compare(output, "") != 0 {
			e2e.Logf("operator ElasticSearch already exist")
			return
		}
		output, err = doAction(oc, "get", asAdmin, withoutNamespace, "crd", crdJaegers.name, "--ignore-not-found")
		o.Expect(err).NotTo(o.HaveOccurred())
		if strings.Compare(output, "") != 0 {
			e2e.Logf("operator Jaeger already exist")
			return
		}

		g.By("create operator ElasticSearch")
		defer crdElasticSearch.delete(oc)
		defer subElasticSearch.delete(itName, dr)
		subElasticSearch.create(oc, itName, dr)
		csvElasticSearch.name = subElasticSearch.installedCSV
		defer csvElasticSearch.delete(itName, dr)
		newCheck("expect", asAdmin, withoutNamespace, compare, "Succeeded", ok, []string{"csv", subElasticSearch.installedCSV, "-n", subElasticSearch.namespace, "-o=jsonpath={.status.phase}"}).check(oc)

		g.By("create operator Jaeger")
		defer crdJaegers.delete(oc)
		defer subJaeger.delete(itName, dr)
		subJaeger.create(oc, itName, dr)
		csvJaeger.name = subJaeger.installedCSV
		defer csvJaeger.delete(itName, dr)
		newCheck("expect", asAdmin, withoutNamespace, compare, "Succeeded", ok, []string{"csv", subJaeger.installedCSV, "-n", subJaeger.namespace, "-o=jsonpath={.status.phase}"}).check(oc)

	})

})
