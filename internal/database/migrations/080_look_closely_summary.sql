-- A merge request now keeps only the summary of its look-closely mark, how
-- many files it lists for each reason ({"reasons": [{"reason", "files"}]}),
-- and none of the change's paths, which come with its diff. Marks kept in
-- the form before are worked out again as devpad starts.
UPDATE merge_requests SET look_closely = '', look_closely_head = '';
