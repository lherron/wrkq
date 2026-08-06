package wrkqapi

type campaignAggregate struct {
	TotalMembers        int
	StateCounts         map[string]int
	ResidentCount       int
	EnrolledCount       int
	InProgressCount     int
	MissingOutcomeCount int
	Footprint           []WrkqCampaignFootprint
	LastActivityAt      string
}
