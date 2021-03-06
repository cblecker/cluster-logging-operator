package fluentd

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	logging "github.com/openshift/cluster-logging-operator/pkg/apis/logging/v1"
	. "github.com/openshift/cluster-logging-operator/test"
	"gopkg.in/yaml.v2"
)

var _ = Describe("Generating fluentd config", func() {
	var (
		forwarder     *logging.ClusterLogForwarderSpec
		forwarderSpec *logging.ForwarderSpec
		generator     *ConfigGenerator
	)
	BeforeEach(func() {
		var err error
		generator, err = NewConfigGenerator(false, false, true)
		Expect(err).To(BeNil())
		Expect(generator).ToNot(BeNil())
		forwarder = &logging.ClusterLogForwarderSpec{
			Outputs: []logging.OutputSpec{
				{
					Type:   logging.OutputTypeElasticsearch,
					Name:   "infra-es",
					URL:    "https://es.svc.infra.cluster:9999",
					Secret: &logging.OutputSecretSpec{Name: "my-infra-secret"},
				},
				{
					Type:   logging.OutputTypeElasticsearch,
					Name:   "apps-es-1",
					URL:    "https://es.svc.messaging.cluster.local:9654",
					Secret: &logging.OutputSecretSpec{Name: "my-es-secret"},
				},
				{
					Type: logging.OutputTypeElasticsearch,
					Name: "apps-es-2",
					URL:  "https://es.svc.messaging.cluster.local2:9654",
					Secret: &logging.OutputSecretSpec{
						Name: "my-other-secret",
					},
				},
				{
					Type: logging.OutputTypeElasticsearch,
					Name: "audit-es",
					URL:  "https://es.svc.audit.cluster:9654",
					Secret: &logging.OutputSecretSpec{
						Name: "my-audit-secret",
					},
				},
			},
			Pipelines: []logging.PipelineSpec{
				{
					Name:       "infra-pipeline",
					InputRefs:  []string{logging.InputNameInfrastructure},
					OutputRefs: []string{"infra-es"},
				},
				{
					Name:       "apps-pipeline",
					InputRefs:  []string{logging.InputNameApplication},
					OutputRefs: []string{"apps-es-1", "apps-es-2"},
				},
				{
					Name:       "audit-pipeline",
					InputRefs:  []string{logging.InputNameAudit},
					OutputRefs: []string{"audit-es"},
				},
			},
		}
	})

	It("should generats container source config for given namespaces only", func() {
		forwarder = &logging.ClusterLogForwarderSpec{
			Outputs: []logging.OutputSpec{
				{
					Type: logging.OutputTypeElasticsearch,
					Name: "apps-es-1",
					URL:  "https://es.svc.messaging.cluster.local:9654",
					Secret: &logging.OutputSecretSpec{
						Name: "my-es-secret",
					},
				},
			},
			Inputs: []logging.InputSpec{
				{
					Name: "myInput",
					Application: &logging.Application{
						Namespaces: []string{"project1-namespace", "project2-namespace"},
					},
				},
			},
			Pipelines: []logging.PipelineSpec{
				{
					Name:       "apps-pipeline",
					InputRefs:  []string{"myInput"},
					OutputRefs: []string{"apps-es-1", "apps-es-2"},
				},
			},
		}
		inputs, namespaces := gatherSources(forwarder)
		results, err := generator.generateSource(inputs, namespaces)
		Expect(err).To(BeNil())
		Expect(results).To(HaveLen(1))
		Expect(results[0]).To(EqualTrimLines(`
# container logs
<source>
  @type tail
  @id container-input
  path /var/log/containers/*_project1-namespace_*.log, /var/log/containers/*_project2-namespace_*.log
  exclude_path ["/var/log/containers/fluentd-*_openshift-logging_*.log", "/var/log/containers/elasticsearch-*_openshift-logging_*.log", "/var/log/containers/kibana-*_openshift-logging_*.log"]
  pos_file "/var/log/es-containers.log.pos"
  refresh_interval 5
  rotate_wait 5
  tag kubernetes.*
  read_from_head "true"
  @label @CONCAT
  <parse>
    @type multi_format
    <pattern>
      format json
      time_format '%Y-%m-%dT%H:%M:%S.%N%Z'
      keep_time_key true
    </pattern>
    <pattern>
      format regexp
      expression /^(?<time>.+) (?<stream>stdout|stderr)( (?<logtag>.))? (?<log>.*)$/
      time_format '%Y-%m-%dT%H:%M:%S.%N%:z'
      keep_time_key true
    </pattern>
  </parse>
</source>
		`))
	})

	It("should exclude source to pipeline labels when there are no pipelines for a given sourceType (e.g. only logs.app)", func() {
		forwarder = &logging.ClusterLogForwarderSpec{
			Outputs: []logging.OutputSpec{
				{
					Type: "fluentdForward",
					Name: "secureforward-receiver",
					URL:  "http://es.svc.messaging.cluster.local:9654",
				},
			},
			Pipelines: []logging.PipelineSpec{
				{
					Name:       "apps-pipeline",
					InputRefs:  []string{logging.InputNameApplication},
					OutputRefs: []string{"secureforward-receiver"},
				},
			},
		}
		results, err := generator.Generate(forwarder, forwarderSpec)
		Expect(err).To(BeNil())
		Expect(results).To(EqualTrimLines(`
			## CLO GENERATED CONFIGURATION ### 
			# This file is a copy of the fluentd configuration entrypoint
			# which should normally be supplied in a configmap.

			<system>
  				log_level "#{ENV['LOG_LEVEL'] || 'warn'}"
			</system>

			# In each section below, pre- and post- includes don't include anything initially;
			# they exist to enable future additions to openshift conf as needed.

			## sources
			## ordered so that syslog always runs last...
			<source>
				@type prometheus
                bind ''
				<ssl>
					enable true
					certificate_path "#{ENV['METRICS_CERT'] || '/etc/fluent/metrics/tls.crt'}"
					private_key_path "#{ENV['METRICS_KEY'] || '/etc/fluent/metrics/tls.key'}"
				</ssl>
			</source>

			<source>
				@type prometheus_monitor
				<labels>
					hostname ${hostname}
				</labels>
			</source>

			# excluding prometheus_tail_monitor
			# since it leaks namespace/pod info
			# via file paths

			# This is considered experimental by the repo
			<source>
				@type prometheus_output_monitor
				<labels>
					hostname ${hostname}
				</labels>
			</source>

			# container logs
			<source>
				@type tail
				@id container-input
				path "/var/log/containers/*.log"
				exclude_path ["/var/log/containers/fluentd-*_openshift-logging_*.log", "/var/log/containers/elasticsearch-*_openshift-logging_*.log", "/var/log/containers/kibana-*_openshift-logging_*.log"]
				pos_file "/var/log/es-containers.log.pos"
				refresh_interval 5
				rotate_wait 5
				tag kubernetes.*
				read_from_head "true"
				@label @CONCAT
				<parse>
				@type multi_format
				<pattern>
					format json
					time_format '%Y-%m-%dT%H:%M:%S.%N%Z'
					keep_time_key true
				</pattern>
				<pattern>
					format regexp
					expression /^(?<time>.+) (?<stream>stdout|stderr)( (?<logtag>.))? (?<log>.*)$/
					time_format '%Y-%m-%dT%H:%M:%S.%N%:z'
					keep_time_key true
				</pattern>
				</parse>
			</source>

			<label @CONCAT>
				<filter kubernetes.**>
				@type concat
				key log
				partial_key logtag
				partial_value P
				separator ''
				</filter>
				<match kubernetes.**>
				@type relabel
				@label @INGRESS
				</match>
			</label>

			#syslog input config here

			<label @INGRESS>
				## filters
				<filter **>
					@type record_modifier
					char_encoding utf-8
				</filter>
				<filter journal>
					@type grep
					<exclude>
						key PRIORITY
						pattern ^7$
					</exclude>
				</filter>
				<match journal>
					@type rewrite_tag_filter
					# skip to @INGRESS label section
					@label @INGRESS
					# see if this is a kibana container for special log handling
					# looks like this:
					# k8s_kibana.a67f366_logging-kibana-1-d90e3_logging_26c51a61-2835-11e6-ad29-fa163e4944d5_f0db49a2
					# we filter these logs through the kibana_transform.conf filter
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_kibana\.
						tag kubernetes.journal.container.kibana
					</rule>
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_logging-eventrouter-[^_]+_
						tag kubernetes.journal.container._default_.kubernetes-event
					</rule>
					# mark logs from default namespace for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_default_
						tag kubernetes.journal.container._default_
					</rule>
					# mark logs from kube-* namespaces for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_kube-(.+)_
						tag kubernetes.journal.container._kube-$1_
					</rule>
					# mark logs from openshift-* namespaces for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_openshift-(.+)_
						tag kubernetes.journal.container._openshift-$1_
					</rule>
					# mark logs from openshift namespace for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_openshift_
						tag kubernetes.journal.container._openshift_
					</rule>
					# mark fluentd container logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_.*fluentd
						tag kubernetes.journal.container.fluentd
					</rule>
					# this is a kubernetes container
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_
						tag kubernetes.journal.container
					</rule>
					# not kubernetes - assume a system log or system container log
					<rule>
						key _TRANSPORT
						pattern .+
						tag journal.system
					</rule>
				</match>
				<filter kubernetes.**>
					@type kubernetes_metadata
					kubernetes_url 'https://kubernetes.default.svc'
					cache_size '1000'
					watch 'false'
					use_journal 'nil'
					ssl_partial_chain 'true'
				</filter>

				<filter kubernetes.journal.**>
					@type parse_json_field
					merge_json_log 'false'
					preserve_json_log 'true'
					json_fields 'log,MESSAGE'
				</filter>

				<filter kubernetes.var.log.containers.**>
					@type parse_json_field
					merge_json_log 'false'
					preserve_json_log 'true'
					json_fields 'log,MESSAGE'
				</filter>

				<filter kubernetes.var.log.containers.eventrouter-** kubernetes.var.log.containers.cluster-logging-eventrouter-**>
					@type parse_json_field
					merge_json_log true
					preserve_json_log true
					json_fields 'log,MESSAGE'
				</filter>

				<filter **kibana**>
					@type record_transformer
					enable_ruby
					<record>
						log ${record['err'] || record['msg'] || record['MESSAGE'] || record['log']}
					</record>
					remove_keys req,res,msg,name,level,v,pid,err
				</filter>
				<filter **>
					@type viaq_data_model
					elasticsearch_index_prefix_field 'viaq_index_name'
					default_keep_fields CEE,time,@timestamp,aushape,ci_job,collectd,docker,fedora-ci,file,foreman,geoip,hostname,ipaddr4,ipaddr6,kubernetes,level,message,namespace_name,namespace_uuid,offset,openstack,ovirt,pid,pipeline_metadata,rsyslog,service,systemd,tags,testcase,tlog,viaq_msg_id
					extra_keep_fields ''
					keep_empty_fields 'message'
					use_undefined false
					undefined_name 'undefined'
					rename_time true
					rename_time_if_missing false
					src_time_name 'time'
					dest_time_name '@timestamp'
					pipeline_type 'collector'
					undefined_to_string 'false'
					undefined_dot_replace_char 'UNUSED'
					undefined_max_num_fields '-1'
					process_kubernetes_events 'false'
					<formatter>
						tag "system.var.log**"
						type sys_var_log
						remove_keys host,pid,ident
					</formatter>
					<formatter>
						tag "journal.system**"
						type sys_journal
						remove_keys log,stream,MESSAGE,_SOURCE_REALTIME_TIMESTAMP,__REALTIME_TIMESTAMP,CONTAINER_ID,CONTAINER_ID_FULL,CONTAINER_NAME,PRIORITY,_BOOT_ID,_CAP_EFFECTIVE,_CMDLINE,_COMM,_EXE,_GID,_HOSTNAME,_MACHINE_ID,_PID,_SELINUX_CONTEXT,_SYSTEMD_CGROUP,_SYSTEMD_SLICE,_SYSTEMD_UNIT,_TRANSPORT,_UID,_AUDIT_LOGINUID,_AUDIT_SESSION,_SYSTEMD_OWNER_UID,_SYSTEMD_SESSION,_SYSTEMD_USER_UNIT,CODE_FILE,CODE_FUNCTION,CODE_LINE,ERRNO,MESSAGE_ID,RESULT,UNIT,_KERNEL_DEVICE,_KERNEL_SUBSYSTEM,_UDEV_SYSNAME,_UDEV_DEVNODE,_UDEV_DEVLINK,SYSLOG_FACILITY,SYSLOG_IDENTIFIER,SYSLOG_PID
					</formatter>
					<formatter>
						tag "kubernetes.journal.container**"
						type k8s_journal
						remove_keys 'log,stream,MESSAGE,_SOURCE_REALTIME_TIMESTAMP,__REALTIME_TIMESTAMP,CONTAINER_ID,CONTAINER_ID_FULL,CONTAINER_NAME,PRIORITY,_BOOT_ID,_CAP_EFFECTIVE,_CMDLINE,_COMM,_EXE,_GID,_HOSTNAME,_MACHINE_ID,_PID,_SELINUX_CONTEXT,_SYSTEMD_CGROUP,_SYSTEMD_SLICE,_SYSTEMD_UNIT,_TRANSPORT,_UID,_AUDIT_LOGINUID,_AUDIT_SESSION,_SYSTEMD_OWNER_UID,_SYSTEMD_SESSION,_SYSTEMD_USER_UNIT,CODE_FILE,CODE_FUNCTION,CODE_LINE,ERRNO,MESSAGE_ID,RESULT,UNIT,_KERNEL_DEVICE,_KERNEL_SUBSYSTEM,_UDEV_SYSNAME,_UDEV_DEVNODE,_UDEV_DEVLINK,SYSLOG_FACILITY,SYSLOG_IDENTIFIER,SYSLOG_PID'
					</formatter>
					<formatter>
						tag "kubernetes.var.log.containers.eventrouter-** kubernetes.var.log.containers.cluster-logging-eventrouter-** k8s-audit.log** openshift-audit.log**"
						type k8s_json_file
						remove_keys log,stream,CONTAINER_ID_FULL,CONTAINER_NAME
						process_kubernetes_events 'true'
					</formatter>
					<formatter>
						tag "kubernetes.var.log.containers**"
						type k8s_json_file
						remove_keys log,stream,CONTAINER_ID_FULL,CONTAINER_NAME
					</formatter>
					<elasticsearch_index_name>
						enabled 'true'
						tag "journal.system** system.var.log** **_default_** **_kube-*_** **_openshift-*_** **_openshift_**"
						name_type static
						static_index_name infra-write
						</elasticsearch_index_name>
						<elasticsearch_index_name>
						enabled 'true'
						tag "linux-audit.log** k8s-audit.log** openshift-audit.log**"
						name_type static
						static_index_name audit-write
						</elasticsearch_index_name>
						<elasticsearch_index_name>
						enabled 'true'
						tag "**"
						name_type static
						static_index_name app-write
					</elasticsearch_index_name>
				</filter>
				<filter **>
					@type elasticsearch_genid_ext
					hash_id_key viaq_msg_id
					alt_key kubernetes.event.metadata.uid
					alt_tags 'kubernetes.var.log.containers.logging-eventrouter-*.** kubernetes.var.log.containers.eventrouter-*.** kubernetes.var.log.containers.cluster-logging-eventrouter-*.** kubernetes.journal.container._default_.kubernetes-event'
				</filter>
				#flatten labels to prevent field explosion in ES
				<filter ** >
					@type record_transformer
					enable_ruby true
					<record>
						kubernetes ${!record['kubernetes'].nil? ? record['kubernetes'].merge({"flat_labels": (record['kubernetes']['labels']||{}).map{|k,v| "#{k}=#{v}"}}) : {} }
					</record>
					remove_keys $.kubernetes.labels
				</filter>


				# Relabel specific source tags to specific intermediary labels for copy processing
				<match kubernetes.**>
					@type relabel
					@label @_APPLICATION
				</match>
				<match **>
					@type stdout
				</match>

			</label>

			# Relabel specific sources (e.g. logs.apps) to multiple pipelines
			<label @_APPLICATION>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @APPS_PIPELINE
					</store>
				</match>
			</label>

			# Relabel specific pipelines to multiple, outputs (e.g. ES, kafka stores)
			<label @APPS_PIPELINE>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @SECUREFORWARD_RECEIVER
					</store>
				</match>
			</label>
			# Ship logs to specific outputs
			<label @SECUREFORWARD_RECEIVER>
				<match **>
					# https://docs.fluentd.org/v1.0/articles/in_forward
				@type forward

				<buffer>
					@type file
					path '/var/lib/fluentd/secureforward_receiver'
					queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '1024' }"
          total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
          chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '1m'}"
          flush_mode interval
					flush_interval 5s
					flush_at_shutdown true
					flush_thread_count 2
          retry_type exponential_backoff
          retry_wait 1s
          retry_max_interval 300s
					retry_forever true
					# the systemd journald 0.0.8 input plugin will just throw away records if the buffer
					# queue limit is hit - 'block' will halt further reads and keep retrying to flush the
					# buffer to the remote - default is 'block' because in_tail handles that case
					overflow_action block
				</buffer>

				<server>
					host es.svc.messaging.cluster.local
					port 9654
				</server>
			</match>
		</label>
	`))
	})

	It("should produce well formed fluent.conf", func() {
		results, err := generator.Generate(forwarder, forwarderSpec)
		Expect(err).To(BeNil())
		Expect(results).To(EqualTrimLines(`
			## CLO GENERATED CONFIGURATION ### 
			# This file is a copy of the fluentd configuration entrypoint
			# which should normally be supplied in a configmap.

			<system>
  				log_level "#{ENV['LOG_LEVEL'] || 'warn'}"
			</system>

			# In each section below, pre- and post- includes don't include anything initially;
			# they exist to enable future additions to openshift conf as needed.

			## sources
			## ordered so that syslog always runs last...
			<source>
				@type prometheus
                bind ''
				<ssl>
					enable true
					certificate_path "#{ENV['METRICS_CERT'] || '/etc/fluent/metrics/tls.crt'}"
					private_key_path "#{ENV['METRICS_KEY'] || '/etc/fluent/metrics/tls.key'}"
				</ssl>
			</source>

			<source>
				@type prometheus_monitor
				<labels>
					hostname ${hostname}
				</labels>
			</source>

			# excluding prometheus_tail_monitor
			# since it leaks namespace/pod info
			# via file paths

			# This is considered experimental by the repo
			<source>
				@type prometheus_output_monitor
				<labels>
					hostname ${hostname}
				</labels>
			</source>

			#journal logs to gather node
			<source>
				@type systemd
				@id systemd-input
				@label @INGRESS
				path '/run/log/journal'
				<storage>
					@type local
					persistent true
					# NOTE: if this does not end in .json, fluentd will think it
					# is the name of a directory - see fluentd storage_local.rb
					path '/var/log/journal_pos.json'
				</storage>
				matches "#{ENV['JOURNAL_FILTERS_JSON'] || '[]'}"
				tag journal
				read_from_head "#{if (val = ENV.fetch('JOURNAL_READ_FROM_HEAD','')) && (val.length > 0); val; else 'false'; end}"
			</source>

			# container logs
			<source>
				@type tail
				@id container-input
				path "/var/log/containers/*.log"
				exclude_path ["/var/log/containers/fluentd-*_openshift-logging_*.log", "/var/log/containers/elasticsearch-*_openshift-logging_*.log", "/var/log/containers/kibana-*_openshift-logging_*.log"]
				pos_file "/var/log/es-containers.log.pos"
				refresh_interval 5
				rotate_wait 5
				tag kubernetes.*
				read_from_head "true"
				@label @CONCAT
				<parse>
				@type multi_format
				<pattern>
					format json
					time_format '%Y-%m-%dT%H:%M:%S.%N%Z'
					keep_time_key true
				</pattern>
				<pattern>
					format regexp
					expression /^(?<time>.+) (?<stream>stdout|stderr)( (?<logtag>.))? (?<log>.*)$/
					time_format '%Y-%m-%dT%H:%M:%S.%N%:z'
					keep_time_key true
				</pattern>
				</parse>
			</source>

            # linux audit logs
            <source>
              @type tail
              @id audit-input
              @label @INGRESS
              path "#{ENV['AUDIT_FILE'] || '/var/log/audit/audit.log'}"
              pos_file "#{ENV['AUDIT_POS_FILE'] || '/var/log/audit/audit.log.pos'}"
              tag linux-audit.log
              <parse>
                @type viaq_host_audit
              </parse>
			</source>

            # k8s audit logs
            <source>
              @type tail
              @id k8s-audit-input
              @label @INGRESS
              path "#{ENV['K8S_AUDIT_FILE'] || '/var/log/kube-apiserver/audit.log'}"
              pos_file "#{ENV['K8S_AUDIT_POS_FILE'] || '/var/log/kube-apiserver/audit.log.pos'}"
              tag k8s-audit.log
              <parse>
                @type json
                time_key requestReceivedTimestamp
                # In case folks want to parse based on the requestReceivedTimestamp key
                keep_time_key true
                time_format %Y-%m-%dT%H:%M:%S.%N%z
			  </parse>
			</source>

            # Openshift audit logs
            <source>
              @type tail
              @id openshift-audit-input
              @label @INGRESS
              path /var/log/oauth-apiserver/audit.log,/var/log/openshift-apiserver/audit.log
              pos_file /var/log/oauth-apiserver.audit.log
              tag openshift-audit.log
              <parse>
                @type json
                time_key requestReceivedTimestamp
                # In case folks want to parse based on the requestReceivedTimestamp key
                keep_time_key true
                time_format %Y-%m-%dT%H:%M:%S.%N%z
              </parse>
            </source>

			<label @CONCAT>
				<filter kubernetes.**>
				@type concat
				key log
				partial_key logtag
				partial_value P
				separator ''
				</filter>
				<match kubernetes.**>
				@type relabel
				@label @INGRESS
				</match>
			</label>

			#syslog input config here

			<label @INGRESS>
				## filters
				<filter **>
					@type record_modifier
					char_encoding utf-8
				</filter>
				<filter journal>
					@type grep
					<exclude>
						key PRIORITY
						pattern ^7$
					</exclude>
				</filter>
				<match journal>
					@type rewrite_tag_filter
					# skip to @INGRESS label section
					@label @INGRESS
					# see if this is a kibana container for special log handling
					# looks like this:
					# k8s_kibana.a67f366_logging-kibana-1-d90e3_logging_26c51a61-2835-11e6-ad29-fa163e4944d5_f0db49a2
					# we filter these logs through the kibana_transform.conf filter
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_kibana\.
						tag kubernetes.journal.container.kibana
					</rule>
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_logging-eventrouter-[^_]+_
						tag kubernetes.journal.container._default_.kubernetes-event
					</rule>
					# mark logs from default namespace for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_default_
						tag kubernetes.journal.container._default_
					</rule>
					# mark logs from kube-* namespaces for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_kube-(.+)_
						tag kubernetes.journal.container._kube-$1_
					</rule>
					# mark logs from openshift-* namespaces for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_openshift-(.+)_
						tag kubernetes.journal.container._openshift-$1_
					</rule>
					# mark logs from openshift namespace for processing as k8s logs but stored as system logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_[^_]+_[^_]+_openshift_
						tag kubernetes.journal.container._openshift_
					</rule>
					# mark fluentd container logs
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_.*fluentd
						tag kubernetes.journal.container.fluentd
					</rule>
					# this is a kubernetes container
					<rule>
						key CONTAINER_NAME
						pattern ^k8s_
						tag kubernetes.journal.container
					</rule>
					# not kubernetes - assume a system log or system container log
					<rule>
						key _TRANSPORT
						pattern .+
						tag journal.system
					</rule>
				</match>
				<filter kubernetes.**>
					@type kubernetes_metadata
					kubernetes_url 'https://kubernetes.default.svc'
					cache_size '1000'
					watch 'false'
					use_journal 'nil'
					ssl_partial_chain 'true'
				</filter>

				<filter kubernetes.journal.**>
					@type parse_json_field
					merge_json_log 'false'
					preserve_json_log 'true'
					json_fields 'log,MESSAGE'
				</filter>

				<filter kubernetes.var.log.containers.**>
					@type parse_json_field
					merge_json_log 'false'
					preserve_json_log 'true'
					json_fields 'log,MESSAGE'
				</filter>

				<filter kubernetes.var.log.containers.eventrouter-** kubernetes.var.log.containers.cluster-logging-eventrouter-**>
					@type parse_json_field
					merge_json_log true
					preserve_json_log true
					json_fields 'log,MESSAGE'
				</filter>

				<filter **kibana**>
					@type record_transformer
					enable_ruby
					<record>
						log ${record['err'] || record['msg'] || record['MESSAGE'] || record['log']}
					</record>
					remove_keys req,res,msg,name,level,v,pid,err
				</filter>
				<filter **>
					@type viaq_data_model
					elasticsearch_index_prefix_field 'viaq_index_name'
					default_keep_fields CEE,time,@timestamp,aushape,ci_job,collectd,docker,fedora-ci,file,foreman,geoip,hostname,ipaddr4,ipaddr6,kubernetes,level,message,namespace_name,namespace_uuid,offset,openstack,ovirt,pid,pipeline_metadata,rsyslog,service,systemd,tags,testcase,tlog,viaq_msg_id
					extra_keep_fields ''
					keep_empty_fields 'message'
					use_undefined false
					undefined_name 'undefined'
					rename_time true
					rename_time_if_missing false
					src_time_name 'time'
					dest_time_name '@timestamp'
					pipeline_type 'collector'
					undefined_to_string 'false'
					undefined_dot_replace_char 'UNUSED'
					undefined_max_num_fields '-1'
					process_kubernetes_events 'false'
					<formatter>
						tag "system.var.log**"
						type sys_var_log
						remove_keys host,pid,ident
					</formatter>
					<formatter>
						tag "journal.system**"
						type sys_journal
						remove_keys log,stream,MESSAGE,_SOURCE_REALTIME_TIMESTAMP,__REALTIME_TIMESTAMP,CONTAINER_ID,CONTAINER_ID_FULL,CONTAINER_NAME,PRIORITY,_BOOT_ID,_CAP_EFFECTIVE,_CMDLINE,_COMM,_EXE,_GID,_HOSTNAME,_MACHINE_ID,_PID,_SELINUX_CONTEXT,_SYSTEMD_CGROUP,_SYSTEMD_SLICE,_SYSTEMD_UNIT,_TRANSPORT,_UID,_AUDIT_LOGINUID,_AUDIT_SESSION,_SYSTEMD_OWNER_UID,_SYSTEMD_SESSION,_SYSTEMD_USER_UNIT,CODE_FILE,CODE_FUNCTION,CODE_LINE,ERRNO,MESSAGE_ID,RESULT,UNIT,_KERNEL_DEVICE,_KERNEL_SUBSYSTEM,_UDEV_SYSNAME,_UDEV_DEVNODE,_UDEV_DEVLINK,SYSLOG_FACILITY,SYSLOG_IDENTIFIER,SYSLOG_PID
					</formatter>
					<formatter>
						tag "kubernetes.journal.container**"
						type k8s_journal
						remove_keys 'log,stream,MESSAGE,_SOURCE_REALTIME_TIMESTAMP,__REALTIME_TIMESTAMP,CONTAINER_ID,CONTAINER_ID_FULL,CONTAINER_NAME,PRIORITY,_BOOT_ID,_CAP_EFFECTIVE,_CMDLINE,_COMM,_EXE,_GID,_HOSTNAME,_MACHINE_ID,_PID,_SELINUX_CONTEXT,_SYSTEMD_CGROUP,_SYSTEMD_SLICE,_SYSTEMD_UNIT,_TRANSPORT,_UID,_AUDIT_LOGINUID,_AUDIT_SESSION,_SYSTEMD_OWNER_UID,_SYSTEMD_SESSION,_SYSTEMD_USER_UNIT,CODE_FILE,CODE_FUNCTION,CODE_LINE,ERRNO,MESSAGE_ID,RESULT,UNIT,_KERNEL_DEVICE,_KERNEL_SUBSYSTEM,_UDEV_SYSNAME,_UDEV_DEVNODE,_UDEV_DEVLINK,SYSLOG_FACILITY,SYSLOG_IDENTIFIER,SYSLOG_PID'
					</formatter>
					<formatter>
						tag "kubernetes.var.log.containers.eventrouter-** kubernetes.var.log.containers.cluster-logging-eventrouter-** k8s-audit.log** openshift-audit.log**"
						type k8s_json_file
						remove_keys log,stream,CONTAINER_ID_FULL,CONTAINER_NAME
						process_kubernetes_events 'true'
					</formatter>
					<formatter>
						tag "kubernetes.var.log.containers**"
						type k8s_json_file
						remove_keys log,stream,CONTAINER_ID_FULL,CONTAINER_NAME
					</formatter>
					<elasticsearch_index_name>
						enabled 'true'
						tag "journal.system** system.var.log** **_default_** **_kube-*_** **_openshift-*_** **_openshift_**"
						name_type static
						static_index_name infra-write
					</elasticsearch_index_name>
					<elasticsearch_index_name>
						enabled 'true'
						tag "linux-audit.log** k8s-audit.log** openshift-audit.log**"
						name_type static
						static_index_name audit-write
					</elasticsearch_index_name>
					<elasticsearch_index_name>
						enabled 'true'
						tag "**"
						name_type static
						static_index_name app-write
					</elasticsearch_index_name>
				</filter>
				<filter **>
					@type elasticsearch_genid_ext
					hash_id_key viaq_msg_id
					alt_key kubernetes.event.metadata.uid
					alt_tags 'kubernetes.var.log.containers.logging-eventrouter-*.** kubernetes.var.log.containers.eventrouter-*.** kubernetes.var.log.containers.cluster-logging-eventrouter-*.** kubernetes.journal.container._default_.kubernetes-event'
				</filter>
				#flatten labels to prevent field explosion in ES
				<filter ** >
					@type record_transformer
					enable_ruby true
					<record>
						kubernetes ${!record['kubernetes'].nil? ? record['kubernetes'].merge({"flat_labels": (record['kubernetes']['labels']||{}).map{|k,v| "#{k}=#{v}"}}) : {} }
					</record>
					remove_keys $.kubernetes.labels
				</filter>

				# Relabel specific source tags to specific intermediary labels for copy processing
				<match **_default_** **_kube-*_** **_openshift-*_** **_openshift_** journal.** system.var.log**>
					@type relabel
					@label @_INFRASTRUCTURE
				</match>
				<match kubernetes.**>
					@type relabel
					@label @_APPLICATION
				</match>
				<match linux-audit.log** k8s-audit.log** openshift-audit.log**>
					@type relabel
					@label @_AUDIT
				</match>
				<match **>
					@type stdout
				</match>
			</label>

			# Relabel specific sources (e.g. logs.apps) to multiple pipelines
			<label @_APPLICATION>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @APPS_PIPELINE
					</store>
				</match>
			</label>
			<label @_AUDIT>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @AUDIT_PIPELINE
					</store>
				</match>
			</label>
			<label @_INFRASTRUCTURE>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @INFRA_PIPELINE
					</store>
				</match>
			</label>

			# Relabel specific pipelines to multiple, outputs (e.g. ES, kafka stores)
			<label @INFRA_PIPELINE>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @INFRA_ES
					</store>
				</match>
			</label>
			<label @APPS_PIPELINE>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @APPS_ES_1
					</store>
					<store>
						@type relabel
						@label @APPS_ES_2
					</store>
				</match>
			</label>
			<label @AUDIT_PIPELINE>
				<match **>
					@type copy
					<store>
						@type relabel
						@label @AUDIT_ES
					</store>
				</match>
			</label>

			# Ship logs to specific outputs
			<label @INFRA_ES>
				<match retry_infra_es>
					@type copy
					<store>
						@type elasticsearch
						@id retry_infra_es
						host es.svc.infra.cluster
						port 9999
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-infra-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-infra-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-infra-secret/ca-bundle.crt'
						type_name _doc
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
					        sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'	
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/retry_infra_es'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
              queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
						  total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
				<match **>
					@type copy
					<store>
						@type elasticsearch
						@id infra_es
						host es.svc.infra.cluster
						port 9999
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-infra-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-infra-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-infra-secret/ca-bundle.crt'
						type_name _doc
						retry_tag retry_infra_es
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
               					sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'	
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/infra_es'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
 							retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
			</label>
			<label @APPS_ES_1>
				<match retry_apps_es_1>
					@type copy
					<store>
						@type elasticsearch
						@id retry_apps_es_1
						host es.svc.messaging.cluster.local
						port 9654
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-es-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-es-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-es-secret/ca-bundle.crt'
						type_name _doc
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
						sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'						
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/retry_apps_es_1'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
				<match **>
					@type copy
					<store>
						@type elasticsearch
						@id apps_es_1
						host es.svc.messaging.cluster.local
						port 9654
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-es-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-es-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-es-secret/ca-bundle.crt'
						type_name _doc
						retry_tag retry_apps_es_1
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
						sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'						
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/apps_es_1'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
			</label>
			<label @APPS_ES_2>
				<match retry_apps_es_2>
					@type copy
					<store>
						@type elasticsearch
						@id retry_apps_es_2
						host es.svc.messaging.cluster.local2
						port 9654
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-other-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-other-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-other-secret/ca-bundle.crt'
						type_name _doc
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
						sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'						
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/retry_apps_es_2'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
				<match **>
					@type copy
					<store>
						@type elasticsearch
						@id apps_es_2
						host es.svc.messaging.cluster.local2
						port 9654
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-other-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-other-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-other-secret/ca-bundle.crt'
						type_name _doc
						retry_tag retry_apps_es_2
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
						sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'						
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/apps_es_2'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
			</label>
			<label @AUDIT_ES>
				<match retry_audit_es>
					@type copy
					<store>
						@type elasticsearch
						@id retry_audit_es
						host es.svc.audit.cluster
						port 9654
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-audit-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-audit-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-audit-secret/ca-bundle.crt'
						type_name _doc
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
						sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'						
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/retry_audit_es'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
				<match **>
					@type copy
					<store>
						@type elasticsearch
						@id audit_es
						host es.svc.audit.cluster
						port 9654
						verify_es_version_at_startup false
						scheme https
						ssl_version TLSv1_2
						target_index_key viaq_index_name
						id_key viaq_msg_id
						remove_keys viaq_index_name
						user fluentd
						password changeme

						client_key '/var/run/ocp-collector/secrets/my-audit-secret/tls.key'
						client_cert '/var/run/ocp-collector/secrets/my-audit-secret/tls.crt'
						ca_file '/var/run/ocp-collector/secrets/my-audit-secret/ca-bundle.crt'
						type_name _doc
						retry_tag retry_audit_es
						write_operation create
						reload_connections 'true'
						# https://github.com/uken/fluent-plugin-elasticsearch#reload-after
						reload_after '200'
						# https://github.com/uken/fluent-plugin-elasticsearch#sniffer-class-name
						sniffer_class_name 'Fluent::Plugin::ElasticsearchSimpleSniffer'						
						reload_on_failure false
						# 2 ^ 31
						request_timeout 2147483648
						<buffer>
							@type file
							path '/var/lib/fluentd/audit_es'
              flush_mode interval
							flush_interval 1s
							flush_thread_count 2
							flush_at_shutdown true
              retry_type exponential_backoff
              retry_wait 1s
              retry_max_interval 300s
							retry_forever true
							queued_chunks_limit_size "#{ENV['BUFFER_QUEUE_LIMIT'] || '32' }"
							total_limit_size "#{ENV['TOTAL_LIMIT_SIZE'] ||  8589934592 }" #8G
							chunk_limit_size "#{ENV['BUFFER_SIZE_LIMIT'] || '8m'}"
							overflow_action block
						</buffer>
					</store>
				</match>
			</label>
			`))
	})

	It("should generate sources for reserved inputs used as names or types", func() {
		sources, _ := gatherSources(&logging.ClusterLogForwarderSpec{
			Inputs: []logging.InputSpec{{Name: "in", Application: &logging.Application{}}},
			Pipelines: []logging.PipelineSpec{
				{
					InputRefs:  []string{"in"},
					OutputRefs: []string{"default"},
				},
				{
					InputRefs:  []string{"audit"},
					OutputRefs: []string{"default"},
				},
			},
		})
		Expect(sources.List()).To(ContainElements("application", "audit"))
	})
	var _ = DescribeTable("Verify generated fluentd.conf for valid forwarder specs",
		func(yamlSpec, wantFluentdConf string) {
			var spec logging.ClusterLogForwarderSpec
			Expect(yaml.Unmarshal([]byte(yamlSpec), &spec)).To(Succeed())
			gotFluentdConf, err := generator.Generate(&spec, nil)
			Expect(err).To(Succeed())
			Expect(wantFluentdConf).To(EqualTrimLines(gotFluentdConf))
		},
		Entry("namespaces", `
  pipelines:
    - name: test-app
      inputrefs:
        - inputest
      outputrefs:
        - default
  inputs:
    - name: inputest
      application:
        namespaces:
         - project1
         - project2
`, `
    ## CLO GENERATED CONFIGURATION ###
    # This file is a copy of the fluentd configuration entrypoint
    # which should normally be supplied in a configmap.
    
    <system>
      log_level "#{ENV['LOG_LEVEL'] || 'warn'}"
    </system>
    
    # In each section below, pre- and post- includes don't include anything initially;
    # they exist to enable future additions to openshift conf as needed.
    
    ## sources
    ## ordered so that syslog always runs last...
    <source>
      @type prometheus
      bind ''
      <ssl>
        enable true
        certificate_path "#{ENV['METRICS_CERT'] || '/etc/fluent/metrics/tls.crt'}"
        private_key_path "#{ENV['METRICS_KEY'] || '/etc/fluent/metrics/tls.key'}"
      </ssl>
    </source>
    
    <source>
      @type prometheus_monitor
      <labels>
        hostname ${hostname}
      </labels>
    </source>
    
    # excluding prometheus_tail_monitor
    # since it leaks namespace/pod info
    # via file paths
    
    # This is considered experimental by the repo
    <source>
      @type prometheus_output_monitor
      <labels>
        hostname ${hostname}
      </labels>
    </source>
    # container logs
    <source>
      @type tail
      @id container-input
      path /var/log/containers/*_project1_*.log, /var/log/containers/*_project2_*.log
      exclude_path ["/var/log/containers/fluentd-*_openshift-logging_*.log", "/var/log/containers/elasticsearch-*_openshift-logging_*.log", "/var/log/containers/kibana-*_openshift-logging_*.log"]
      pos_file "/var/log/es-containers.log.pos"
      refresh_interval 5
      rotate_wait 5
      tag kubernetes.*
      read_from_head "true"
      @label @CONCAT
      <parse>
        @type multi_format
        <pattern>
          format json
          time_format '%Y-%m-%dT%H:%M:%S.%N%Z'
          keep_time_key true
        </pattern>
        <pattern>
          format regexp
          expression /^(?<time>.+) (?<stream>stdout|stderr)( (?<logtag>.))? (?<log>.*)$/
          time_format '%Y-%m-%dT%H:%M:%S.%N%:z'
          keep_time_key true
        </pattern>
      </parse>
    </source>
    
    <label @CONCAT>
      <filter kubernetes.**>
        @type concat
        key log
        partial_key logtag
        partial_value P
        separator ''
      </filter>
      <match kubernetes.**>
        @type relabel
        @label @INGRESS
      </match>
    </label>
    
    #syslog input config here
    
    <label @INGRESS>
    
      ## filters
      <filter **>
        @type record_modifier
        char_encoding utf-8
      </filter>
    
      <filter journal>
        @type grep
        <exclude>
          key PRIORITY
          pattern ^7$
        </exclude>
      </filter>
    
      <match journal>
        @type rewrite_tag_filter
        # skip to @INGRESS label section
        @label @INGRESS
    
        # see if this is a kibana container for special log handling
        # looks like this:
        # k8s_kibana.a67f366_logging-kibana-1-d90e3_logging_26c51a61-2835-11e6-ad29-fa163e4944d5_f0db49a2
        # we filter these logs through the kibana_transform.conf filter
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_kibana\.
          tag kubernetes.journal.container.kibana
        </rule>
    
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_[^_]+_logging-eventrouter-[^_]+_
          tag kubernetes.journal.container._default_.kubernetes-event
        </rule>
    
        # mark logs from default namespace for processing as k8s logs but stored as system logs
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_[^_]+_[^_]+_default_
          tag kubernetes.journal.container._default_
        </rule>
    
        # mark logs from kube-* namespaces for processing as k8s logs but stored as system logs
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_[^_]+_[^_]+_kube-(.+)_
          tag kubernetes.journal.container._kube-$1_
        </rule>
    
        # mark logs from openshift-* namespaces for processing as k8s logs but stored as system logs
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_[^_]+_[^_]+_openshift-(.+)_
          tag kubernetes.journal.container._openshift-$1_
        </rule>
    
        # mark logs from openshift namespace for processing as k8s logs but stored as system logs
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_[^_]+_[^_]+_openshift_
          tag kubernetes.journal.container._openshift_
        </rule>
    
        # mark fluentd container logs
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_.*fluentd
          tag kubernetes.journal.container.fluentd
        </rule>
    
        # this is a kubernetes container
        <rule>
          key CONTAINER_NAME
          pattern ^k8s_
          tag kubernetes.journal.container
        </rule>
    
        # not kubernetes - assume a system log or system container log
        <rule>
          key _TRANSPORT
          pattern .+
          tag journal.system
        </rule>
      </match>
    
      <filter kubernetes.**>
        @type kubernetes_metadata
        kubernetes_url 'https://kubernetes.default.svc'
        cache_size '1000'
        watch 'false'
        use_journal 'nil'
        ssl_partial_chain 'true'
      </filter>
    
      <filter kubernetes.journal.**>
        @type parse_json_field
        merge_json_log 'false'
        preserve_json_log 'true'
        json_fields 'log,MESSAGE'
      </filter>
    
      <filter kubernetes.var.log.containers.**>
        @type parse_json_field
        merge_json_log 'false'
        preserve_json_log 'true'
        json_fields 'log,MESSAGE'
      </filter>
    
      <filter kubernetes.var.log.containers.eventrouter-** kubernetes.var.log.containers.cluster-logging-eventrouter-**>
        @type parse_json_field
        merge_json_log true
        preserve_json_log true
        json_fields 'log,MESSAGE'
      </filter>
    
      <filter **kibana**>
        @type record_transformer
        enable_ruby
        <record>
          log ${record['err'] || record['msg'] || record['MESSAGE'] || record['log']}
        </record>
        remove_keys req,res,msg,name,level,v,pid,err
      </filter>
    
      <filter **>
        @type viaq_data_model
        elasticsearch_index_prefix_field 'viaq_index_name'
        default_keep_fields CEE,time,@timestamp,aushape,ci_job,collectd,docker,fedora-ci,file,foreman,geoip,hostname,ipaddr4,ipaddr6,kubernetes,level,message,namespace_name,namespace_uuid,offset,openstack,ovirt,pid,pipeline_metadata,rsyslog,service,systemd,tags,testcase,tlog,viaq_msg_id
        extra_keep_fields ''
        keep_empty_fields 'message'
        use_undefined false
        undefined_name 'undefined'
        rename_time true
        rename_time_if_missing false
        src_time_name 'time'
        dest_time_name '@timestamp'
        pipeline_type 'collector'
        undefined_to_string 'false'
        undefined_dot_replace_char 'UNUSED'
        undefined_max_num_fields '-1'
        process_kubernetes_events 'false'
        <formatter>
          tag "system.var.log**"
          type sys_var_log
          remove_keys host,pid,ident
        </formatter>
        <formatter>
          tag "journal.system**"
          type sys_journal
          remove_keys log,stream,MESSAGE,_SOURCE_REALTIME_TIMESTAMP,__REALTIME_TIMESTAMP,CONTAINER_ID,CONTAINER_ID_FULL,CONTAINER_NAME,PRIORITY,_BOOT_ID,_CAP_EFFECTIVE,_CMDLINE,_COMM,_EXE,_GID,_HOSTNAME,_MACHINE_ID,_PID,_SELINUX_CONTEXT,_SYSTEMD_CGROUP,_SYSTEMD_SLICE,_SYSTEMD_UNIT,_TRANSPORT,_UID,_AUDIT_LOGINUID,_AUDIT_SESSION,_SYSTEMD_OWNER_UID,_SYSTEMD_SESSION,_SYSTEMD_USER_UNIT,CODE_FILE,CODE_FUNCTION,CODE_LINE,ERRNO,MESSAGE_ID,RESULT,UNIT,_KERNEL_DEVICE,_KERNEL_SUBSYSTEM,_UDEV_SYSNAME,_UDEV_DEVNODE,_UDEV_DEVLINK,SYSLOG_FACILITY,SYSLOG_IDENTIFIER,SYSLOG_PID
        </formatter>
        <formatter>
          tag "kubernetes.journal.container**"
          type k8s_journal
          remove_keys 'log,stream,MESSAGE,_SOURCE_REALTIME_TIMESTAMP,__REALTIME_TIMESTAMP,CONTAINER_ID,CONTAINER_ID_FULL,CONTAINER_NAME,PRIORITY,_BOOT_ID,_CAP_EFFECTIVE,_CMDLINE,_COMM,_EXE,_GID,_HOSTNAME,_MACHINE_ID,_PID,_SELINUX_CONTEXT,_SYSTEMD_CGROUP,_SYSTEMD_SLICE,_SYSTEMD_UNIT,_TRANSPORT,_UID,_AUDIT_LOGINUID,_AUDIT_SESSION,_SYSTEMD_OWNER_UID,_SYSTEMD_SESSION,_SYSTEMD_USER_UNIT,CODE_FILE,CODE_FUNCTION,CODE_LINE,ERRNO,MESSAGE_ID,RESULT,UNIT,_KERNEL_DEVICE,_KERNEL_SUBSYSTEM,_UDEV_SYSNAME,_UDEV_DEVNODE,_UDEV_DEVLINK,SYSLOG_FACILITY,SYSLOG_IDENTIFIER,SYSLOG_PID'
        </formatter>
        <formatter>
          tag "kubernetes.var.log.containers.eventrouter-** kubernetes.var.log.containers.cluster-logging-eventrouter-** k8s-audit.log** openshift-audit.log**"
          type k8s_json_file
          remove_keys log,stream,CONTAINER_ID_FULL,CONTAINER_NAME
          process_kubernetes_events 'true'
        </formatter>
        <formatter>
          tag "kubernetes.var.log.containers**"
          type k8s_json_file
          remove_keys log,stream,CONTAINER_ID_FULL,CONTAINER_NAME
        </formatter>
        <elasticsearch_index_name>
          enabled 'true'
          tag "journal.system** system.var.log** **_default_** **_kube-*_** **_openshift-*_** **_openshift_**"
          name_type static
          static_index_name infra-write
        </elasticsearch_index_name>
        <elasticsearch_index_name>
          enabled 'true'
          tag "linux-audit.log** k8s-audit.log** openshift-audit.log**"
          name_type static
          static_index_name audit-write
        </elasticsearch_index_name>
        <elasticsearch_index_name>
          enabled 'true'
          tag "**"
          name_type static
          static_index_name app-write
        </elasticsearch_index_name>
      </filter>
    
      <filter **>
        @type elasticsearch_genid_ext
        hash_id_key viaq_msg_id
        alt_key kubernetes.event.metadata.uid
        alt_tags 'kubernetes.var.log.containers.logging-eventrouter-*.** kubernetes.var.log.containers.eventrouter-*.** kubernetes.var.log.containers.cluster-logging-eventrouter-*.** kubernetes.journal.container._default_.kubernetes-event'
      </filter>
    
      #flatten labels to prevent field explosion in ES
      <filter ** >
        @type record_transformer
        enable_ruby true
        <record>
          kubernetes ${!record['kubernetes'].nil? ? record['kubernetes'].merge({"flat_labels": (record['kubernetes']['labels']||{}).map{|k,v| "#{k}=#{v}"}}) : {} }
        </record>
        remove_keys $.kubernetes.labels
      </filter>
    
      # Relabel specific source tags to specific intermediary labels for copy processing
    
    
      <match kubernetes.**>
        @type relabel
        @label @_APPLICATION
      </match>
    
    
      <match **>
        @type stdout
      </match>
    
    </label>
    
    # Relabel specific sources (e.g. logs.apps) to multiple pipelines
    <label @_APPLICATION>
      <match **>
        @type copy
    
        <store>
          @type relabel
          @label @TEST_APP
        </store>
    
    
      </match>
    </label>
    
    
    # Relabel specific pipelines to multiple, outputs (e.g. ES, kafka stores)
    <label @TEST_APP>
      <match **>
        @type copy
    
        <store>
          @type relabel
          @label @DEFAULT
        </store>
      </match>
    </label>
    
    # Ship logs to specific outputs
`))
})
