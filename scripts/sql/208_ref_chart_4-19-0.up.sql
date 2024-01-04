UPDATE chart_ref SET is_default=false;
INSERT INTO "public"."chart_ref" ("name","location", "version", "deployment_strategy_path","is_default", "active", "created_on", "created_by", "updated_on", "updated_by") VALUES
     ('Deployment','deployment-chart_4-19-0', '4.19.0','pipeline-values.yaml','t', 't', 'now()', 1, 'now()', 1);

INSERT INTO global_strategy_metadata_chart_ref_mapping ("global_strategy_metadata_id", "chart_ref_id", "active", "created_on", "created_by", "updated_on", "updated_by","default")
VALUES (1,(select id from chart_ref where version='4.19.0' and name='Deployment'), true, now(), 1, now(), 1,true),
(4,(select id from chart_ref where version='4.19.0' and name='Deployment'), true, now(), 1, now(), 1,false);


INSERT INTO "public"."chart_ref" ("location", "version","deployment_strategy_path", "is_default", "active", "created_on", "created_by", "updated_on", "updated_by") VALUES
    ('reference-chart_4-19-0', '4.19.0','pipeline-values.yaml', 'f', 't', 'now()', 1, 'now()', 1);

INSERT INTO global_strategy_metadata_chart_ref_mapping ("global_strategy_metadata_id", "chart_ref_id", "active", "created_on", "created_by", "updated_on", "updated_by","default")
VALUES (1,(select id from chart_ref where version='4.19.0' and name is null), true, now(), 1, now(), 1,true),
(2,(select id from chart_ref where version='4.19.0' and name is null), true, now(), 1, now(), 1,false),
(3,(select id from chart_ref where version='4.19.0' and name is null), true, now(), 1, now(), 1,false),
(4,(select id from chart_ref where version='4.19.0' and name is null), true, now(), 1, now(), 1,false);

INSERT INTO "public"."chart_ref" ("location", "version","deployment_strategy_path", "is_default", "active", "created_on", "created_by", "updated_on", "updated_by","name") VALUES
    ('statefulset-chart_4-19-0', '4.19.0','pipeline-values.yaml', 'f', 't', 'now()', 1, 'now()', 1,'StatefulSet');

INSERT INTO global_strategy_metadata_chart_ref_mapping ("global_strategy_metadata_id","chart_ref_id", "active","default","created_on", "created_by", "updated_on", "updated_by") VALUES 
((select id from global_strategy_metadata where name='ROLLINGUPDATE') ,(select id from chart_ref where location='statefulset-chart_4-19-0'), true,true,now(), 1, now(), 1),
((select id from global_strategy_metadata where name='ONDELETE') ,(select id from chart_ref where location='statefulset-chart_4-19-0'), true, false,now(), 1, now(), 1);

