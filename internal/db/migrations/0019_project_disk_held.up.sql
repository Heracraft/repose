-- The bytes a project's volume holds (DECISIONS I-585): the host thin
-- volume's allocated blocks from the newest sample that measured them,
-- which is what the plan's disk counts. A new project starts with an
-- estimate (the api's NewProjectHeldBytes, or a restore's or fork's
-- source figure) and the meter ingest replaces it with each sample.
-- disk_held_at is the sample's time, null while the figure is an
-- estimate. A row with neither counts at its volume_bytes.
alter table projects add column disk_held_bytes bigint;
alter table projects add column disk_held_at timestamptz;

update projects p set disk_held_bytes = l.disk_used, disk_held_at = l.ts
  from (select distinct on (project_id) project_id, ts, disk_used
          from meter_samples where disk_used > 0
         order by project_id, ts desc) l
 where l.project_id = p.id;
